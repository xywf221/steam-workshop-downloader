"""Monkey-patch ``steam.client.cdn.CDNClient.get_chunk`` to use the DLL.

We don't replace the network or AES decryption — only the final
decompression step. Patching keeps ``steam[client]`` as a thin transport
and confines the ctypes work to a single chokepoint.
"""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from swd.dll import decompress


def patch_cdn_client_get_chunk() -> Callable[..., bytes]:
    """Patch :class:`steam.client.cdn.CDNClient.get_chunk` in place.

    Returns the new function (mostly so tests can grab it for inspection).
    """
    from steam.client.cdn import CDNClient
    from steam.core.crypto import symmetric_decrypt
    from steam.exceptions import SteamError

    def patched_get_chunk(self: Any, app_id: int, depot_id: int, chunk_id: str) -> bytes:
        cache_key = (depot_id, chunk_id)
        if cache_key not in self._chunk_cache:
            resp = self.cdn_cmd("depot", f"{depot_id}/chunk/{chunk_id}")
            encrypted = symmetric_decrypt(resp.content, self.get_depot_key(app_id, depot_id))
            try:
                data = decompress(encrypted)
            except Exception as e:
                raise SteamError(f"DLL decompress: {e}") from e
            self._chunk_cache[cache_key] = data
        return self._chunk_cache[cache_key]

    CDNClient.get_chunk = patched_get_chunk
    return patched_get_chunk


__all__ = ["patch_cdn_client_get_chunk"]
