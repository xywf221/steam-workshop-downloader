"""Process-wide anonymous Steam session for the web process.

SteamClient is gevent-based. Flask's default server is multi-threaded.
Calling ``send_um_and_wait`` from random worker threads races the gevent hub
and eventually wedges the CM connection (symptoms: first QueryFiles works,
later pages / GetDetails hang until UM timeout).

All Steam work is therefore funnelled through a **single dedicated thread**
that owns the client (+ CDN) for the process lifetime.
"""

from __future__ import annotations

import contextlib
import queue
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, TypeVar

from swd.constants import UM_RETRIES, UM_TIMEOUT
from swd.steam import init_session
from swd.ui.log import Log

T = TypeVar("T")

# Extra slack beyond UM budget so the worker can finish retries before we give up.
_WORKER_WAIT_S = float(UM_TIMEOUT * UM_RETRIES + 60)


@dataclass
class _Job:
    fn: Callable[..., Any]
    args: tuple
    kwargs: dict
    box: dict
    done: threading.Event
    timeout: float | None


class SteamSession:
    """Hold one anonymous :class:`SteamClient` (+ CDN) on a private worker thread.

    Use **separate** instances for browse vs downloads so a long download never
    blocks QueryFiles / GetDetails (same gevent client cannot serve both).
    """

    def __init__(
        self,
        proxy_url: str | None,
        log: Log,
        *,
        name: str = "steam",
    ) -> None:
        self._proxy_url = proxy_url
        self._log = log
        self._name = name
        self._client: Any | None = None
        self._cdn: Any | None = None
        self._jobs: queue.Queue[_Job | None] = queue.Queue()
        self._thread = threading.Thread(
            target=self._worker_loop,
            name=f"swd-{name}",
            daemon=True,
        )
        self._started = threading.Event()
        self._thread.start()
        # Wait briefly for the thread to boot (not for Steam login).
        self._started.wait(timeout=5)

    @property
    def has_client(self) -> bool:
        return self._client is not None

    @property
    def logged_on(self) -> bool:
        client = self._client
        if client is None:
            return False
        try:
            return bool(getattr(client, "connected", False) and getattr(client, "logged_on", False))
        except Exception:
            return False

    def ensure(self) -> Any:
        """Ensure a live client exists (runs on the Steam worker thread)."""
        return self.call(lambda client: client)

    def call(
        self,
        fn: Callable[..., T],
        *args: Any,
        timeout: float | None = _WORKER_WAIT_S,
        **kwargs: Any,
    ) -> T:
        """Run ``fn(client, *args, **kwargs)`` on the Steam worker thread.

        ``timeout=None`` waits indefinitely (needed for long downloads).
        """
        if threading.current_thread() is self._thread:
            # Already on worker (e.g. nested) — run inline.
            client = self._ensure_client()
            return fn(client, *args, **kwargs)

        box: dict[str, Any] = {}
        done = threading.Event()
        self._jobs.put(
            _Job(fn=fn, args=args, kwargs=kwargs, box=box, done=done, timeout=timeout)
        )
        if not done.wait(timeout=timeout):
            raise TimeoutError(
                f"Steam worker [{self._name}] did not finish within {timeout:.0f}s "
                f"(UM may be wedged; restart swd-web)"
            )
        if "error" in box:
            raise box["error"]
        return box["result"]

    def call_with_cdn(
        self,
        fn: Callable[..., T],
        *args: Any,
        timeout: float | None = _WORKER_WAIT_S,
        **kwargs: Any,
    ) -> T:
        """Like :meth:`call`, but ``fn(client, cdn, *args, **kwargs)``."""

        def _wrap(client: Any, *a: Any, **k: Any) -> T:
            cdn = self._cdn
            if cdn is None:
                raise RuntimeError("CDN client not ready")
            return fn(client, cdn, *a, **k)

        return self.call(_wrap, *args, timeout=timeout, **kwargs)

    def _worker_loop(self) -> None:
        self._started.set()
        while True:
            job = self._jobs.get()
            if job is None:
                return
            try:
                client = self._ensure_client()
                job.box["result"] = job.fn(client, *job.args, **job.kwargs)
            except Exception as e:
                job.box["error"] = e
            finally:
                job.done.set()

    def _ensure_client(self) -> Any:
        """Must only be called on the Steam worker thread."""
        if self._client is not None:
            try:
                if getattr(self._client, "connected", False) and getattr(
                    self._client, "logged_on", False
                ):
                    return self._client
            except Exception:
                pass
            self._log.warn(f"Steam session [{self._name}] dead; re-login on worker thread")
            with contextlib.suppress(Exception):
                self._client.logout()
            self._client = None
            self._cdn = None

        client, cdn = init_session(self._proxy_url, self._log)
        if client is None:
            raise RuntimeError(f"Steam anonymous login failed [{self._name}]")
        self._client = client
        self._cdn = cdn
        return client

    def warmup(self) -> None:
        """Eager login so the first browser hit is not paying CM connect cost."""
        t0 = time.perf_counter()
        try:
            self.ensure()
            self._log.ok(f"Steam ready [{self._name}] ({time.perf_counter() - t0:.1f}s)")
        except Exception as e:
            self._log.err(f"Steam warmup failed [{self._name}]: {e}")


__all__ = ["SteamSession"]
