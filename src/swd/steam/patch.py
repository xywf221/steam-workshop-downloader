"""Monkey-patches for ``steam.client.cdn.CDNClient``.

Two patches:

1. :func:`patch_cdn_client_get_chunk` — swap the final decompression step
   for our ``ctypes`` call into ``steamclient64.dll``. Network + AES stay
   with the steam library. Serialised under a lock so multi-file threads
   share one CDN session safely.
2. :func:`patch_cdn_client_get_manifest` — harden
   ``get_manifest_for_workshop_item`` against the upstream bug where a
   timed-out ``send_um_and_wait`` returns ``None`` and the next line
   crashes on ``resp.header``. Also uses a longer timeout + retries,
   which matter a lot when traffic is routed through a high-latency proxy.
"""

from __future__ import annotations

import threading
import time
from collections.abc import Callable
from typing import Any

from swd.constants import UM_RETRIES, UM_TIMEOUT
from swd.dll import decompress
from swd.utils import compute_backoff

# Serialises CDNClient network + cache + depot-key + DLL decompress.
# steam's requests.Session / servers deque / SteamClient gevent loop are not
# thread-safe; multi-file download holds this lock only around get_chunk.
_cdn_io_lock = threading.RLock()


def patch_cdn_client_get_chunk() -> Callable[..., bytes]:
    """Patch :class:`steam.client.cdn.CDNClient.get_chunk` in place.

    Thread-safe for concurrent multi-file downloads: cache lookup, HTTP
    fetch, depot-key fetch, AES decrypt, and DLL decompress all run under
    :data:`_cdn_io_lock`. Returns the new function (for tests).
    """
    from steam.client.cdn import CDNClient
    from steam.core.crypto import symmetric_decrypt
    from steam.exceptions import SteamError

    def patched_get_chunk(self: Any, app_id: int, depot_id: int, chunk_id: str) -> bytes:
        cache_key = (depot_id, chunk_id)
        with _cdn_io_lock:
            cached = self._chunk_cache.get(cache_key)
            if cached is not None:
                return cached
            resp = self.cdn_cmd("depot", f"{depot_id}/chunk/{chunk_id}")
            encrypted = symmetric_decrypt(resp.content, self.get_depot_key(app_id, depot_id))
            try:
                data = decompress(encrypted)
            except Exception as e:
                raise SteamError(f"DLL decompress: {e}") from e
            self._chunk_cache[cache_key] = data
            return data

    CDNClient.get_chunk = patched_get_chunk
    return patched_get_chunk


def patch_cdn_client_get_manifest() -> Callable[..., Any]:
    """Patch :meth:`CDNClient.get_manifest_for_workshop_item` in place.

    Upstream steam 1.4.4 does::

        resp = self.steam.send_um_and_wait(..., timeout=7)
        if resp.header.eresult != EResult.OK:  # AttributeError if resp is None

    Over a proxy, the 7 s budget is frequently exceeded, so we:

    * bump the timeout to :data:`UM_TIMEOUT`
    * retry :data:`UM_RETRIES` times with exponential backoff
    * treat a ``None`` response as a clean timeout (not a crash)
    * re-login anonymously if the CM connection dropped mid-call

    Returns the new function for tests.
    """
    from steam.client.cdn import CDNClient
    from steam.enums import EResult
    from steam.exceptions import ManifestError, SteamError

    def _ensure_logged_on(steam: Any) -> None:
        """Best-effort reconnect + anonymous re-login if the session died."""
        try:
            connected = bool(getattr(steam, "connected", False))
            logged_on = bool(getattr(steam, "logged_on", False))
        except Exception:
            connected = logged_on = False
        if connected and logged_on:
            return
        try:
            if not connected:
                steam.reconnect(maxdelay=10, retry=3)
            if not getattr(steam, "logged_on", False):
                steam.anonymous_login()
        except Exception:
            # Caller will see the next UM call fail and retry / raise.
            pass

    def patched_get_manifest_for_workshop_item(self: Any, item_id: int) -> Any:
        last_err: Exception | None = None
        for attempt in range(1, UM_RETRIES + 1):
            _ensure_logged_on(self.steam)
            resp = self.steam.send_um_and_wait(
                "PublishedFile.GetDetails#1",
                {
                    "publishedfileids": [item_id],
                    "includetags": False,
                    "includeadditionalpreviews": False,
                    "includechildren": False,
                    "includekvtags": False,
                    "includevotes": False,
                    "short_description": True,
                    "includeforsaledata": False,
                    "includemetadata": False,
                    "language": 0,
                },
                timeout=UM_TIMEOUT,
            )

            if resp is None:
                last_err = SteamError(
                    f"PublishedFile.GetDetails timed out for {item_id} "
                    f"(attempt {attempt}/{UM_RETRIES}, timeout={UM_TIMEOUT}s)",
                    EResult.Timeout,
                )
                if attempt < UM_RETRIES:
                    time.sleep(compute_backoff(attempt))
                    continue
                raise last_err

            if resp.header.eresult != EResult.OK:
                raise SteamError(
                    resp.header.error_message or "No message",
                    resp.header.eresult,
                )

            wf = resp.body.publishedfiledetails[0] if resp.body.publishedfiledetails else None
            if wf is None or wf.result != EResult.OK:
                raise SteamError(
                    "Failed getting workshop file info",
                    EResult.Timeout if wf is None else EResult(wf.result),
                )
            if not wf.hcontent_file:
                raise SteamError("Workshop file is not on SteamPipe", EResult.FileNotFound)

            app_id = ws_app_id = wf.consumer_appid
            try:
                manifest_code = self.get_manifest_request_code(
                    app_id, ws_app_id, int(wf.hcontent_file)
                )
                manifest = self.get_manifest(
                    app_id,
                    ws_app_id,
                    wf.hcontent_file,
                    manifest_request_code=manifest_code,
                )
            except SteamError as exc:
                # Match upstream: wrap as ManifestError so callers can
                # distinguish "item lookup failed" from "manifest fetch failed".
                raise ManifestError(
                    "Failed to acquire manifest",
                    app_id,
                    ws_app_id,
                    int(wf.hcontent_file),
                    exc,
                ) from exc

            manifest.name = wf.title
            return manifest

        # Unreachable — loop either returns or raises — but keep mypy happy.
        assert last_err is not None
        raise last_err

    CDNClient.get_manifest_for_workshop_item = patched_get_manifest_for_workshop_item
    return patched_get_manifest_for_workshop_item


def apply_steam_patches() -> None:
    """Apply every CDNClient patch. Idempotent."""
    patch_cdn_client_get_chunk()
    patch_cdn_client_get_manifest()


__all__ = [
    "apply_steam_patches",
    "patch_cdn_client_get_chunk",
    "patch_cdn_client_get_manifest",
]
