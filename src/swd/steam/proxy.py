"""Proxy URL parsing + ``pysocks`` wiring.

Layers:

- :func:`parse_proxy_url` — pure function (no I/O, easy to unit-test).
- :func:`setup_proxy` — configures global PySocks + stdlib ``socket.socket``.
- :func:`patch_steam_cm_tcp` — routes ValvePython Steam **CM** TCP through
  PySocks. Required because CM uses ``gevent.socket``, which ignores the
  stdlib ``socket.socket = socks.socksocket`` assignment.
"""

from __future__ import annotations

import contextlib
import re
from collections.abc import Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    import socks  # noqa: F401  (type-only import; runtime import is lazy)


_SCHEME_RE = re.compile(r"^([a-zA-Z][a-zA-Z0-9+.\-]*)://(.*)$")

# pysocks protocol constants — strings here avoid a hard import at module load
# time (lets the test suite run without pysocks installed if needed).
_PROTOCOL_BY_SCHEME = {
    "socks5": "SOCKS5",
    "socks5h": "SOCKS5",
    "socks4": "SOCKS4",
    "http": "HTTP",
    "https": "HTTP",
}


@dataclass(frozen=True)
class ParsedProxy:
    """Parsed proxy URL ready to hand to ``pysocks.set_default_proxy``."""

    proto_attr: str  # ``"SOCKS5"`` / ``"SOCKS4"`` / ``"HTTP"``
    host: str
    port: int
    username: str | None
    password: str | None
    rdns: bool = True

    scheme: str = ""
    original: str = ""


def parse_proxy_url(url: str) -> ParsedProxy:
    """Parse a proxy URL into a :class:`ParsedProxy`.

    Supported schemes (case-insensitive):

    ===============  =======================================================
    Scheme           Protocol
    ===============  =======================================================
    ``socks5://``    SOCKS5 (default if scheme omitted)
    ``socks5h://``   SOCKS5 (remote DNS; same as ``socks5://`` here)
    ``socks4://``    SOCKS4
    ``http://``      HTTP CONNECT
    ``https://``     HTTP CONNECT
    bare ``host:port``  SOCKS5
    ===============  =======================================================

    For HTTP/HTTPS proxies, ``user:password@`` is split out and forwarded as
    basic-auth on the CONNECT request.
    """
    raw = url.strip()
    m = _SCHEME_RE.match(raw)
    if m:
        scheme = m.group(1).lower()
        rest = m.group(2)
    else:
        scheme = "socks5"
        rest = raw

    if scheme not in _PROTOCOL_BY_SCHEME:
        raise ValueError(
            f"Unsupported proxy scheme {scheme!r} in {url!r} "
            "(expected socks5://, socks4://, or http(s)://)"
        )

    # Split userinfo (only meaningful for HTTP CONNECT).
    user: str | None = None
    password: str | None = None
    if "@" in rest:
        userinfo, rest = rest.rsplit("@", 1)
        if ":" in userinfo:
            user, password = userinfo.split(":", 1)
        else:
            user = userinfo

    if ":" not in rest:
        raise ValueError(
            f"Invalid proxy URL {url!r}: expected host:port "
            "(e.g. socks5://127.0.0.1:1080 or http://user:pass@proxy:8080)"
        )
    host, port = rest.rsplit(":", 1)
    try:
        port_num = int(port)
    except ValueError as e:
        raise ValueError(f"Invalid proxy port in {url!r}: {port!r}") from e

    return ParsedProxy(
        proto_attr=_PROTOCOL_BY_SCHEME[scheme],
        host=host,
        port=port_num,
        username=user,
        password=password,
        rdns=True,
        scheme=scheme,
        original=url,
    )


def _make_getaddrinfo_wrapper(
    original: Callable[..., list[tuple]],
) -> Callable[..., list[tuple]]:
    """Wrap ``socket.getaddrinfo`` to fall back to ``gethostbyname`` when
    ``getaddrinfo`` fails (known issue on some networks, e.g. IPv6 dual-stack
    misconfiguration).

    This is needed because ``steam[client]`` uses ``socket.getaddrinfo`` for
    CM server discovery, and on some networks it returns error 10044 for
    ``cm0.steampowered.com`` even though ``gethostbyname`` resolves fine.
    """
    import socket as _socket

    def getaddrinfo(
        host: str,
        port: int,
        family: int = 0,
        socktype: int = 0,
        proto: int = 0,
        flags: int = 0,
    ) -> list[tuple]:
        try:
            return original(host, port, family, socktype, proto, flags)
        except OSError:
            # Fallback: resolve via gethostbyname (IPv4 only), then
            # construct a getaddrinfo-style result list.
            ip = _socket.gethostbyname(host)
            if family == 0:
                family = _socket.AF_INET
            if socktype == 0:
                socktype = _socket.SOCK_STREAM
            if proto == 0:
                proto = _socket.IPPROTO_TCP
            return [(family, socktype, proto, "", (ip, port))]

    return getaddrinfo


def patch_steam_cm_tcp() -> bool:
    """Route Steam CM TCP through the configured PySocks default proxy.

    ValvePython's CM client (``steam.core.connection.TCPConnection``) builds
    sockets with ``gevent.socket.socket``, which allocates a **raw**
    ``_socket.socket``. That path never sees
    ``socket.socket = socks.socksocket``, so without this patch:

    * CDN HTTP / media urllib may use the proxy
    * ``PublishedFile.QueryFiles`` / ``GetDetails`` CM traffic goes **direct**
      and often times out behind a GFW / restricted network

    Implementation: perform the SOCKS/HTTP-CONNECT handshake with a blocking
    ``socks.socksocket`` (honours ``socks.set_default_proxy``), ``detach()``
    the FD, then wrap it in a non-blocking ``gevent.socket`` for the CM
    reader/writer greenlets.

    Returns ``True`` if the patch is active (or already was). ``False`` if
    steam/gevent/pysocks are unavailable.
    """
    try:
        import socket as std_socket

        import socks
        from gevent import socket as gsocket
        from steam.core.connection import TCPConnection
    except ImportError:
        return False

    if getattr(TCPConnection, "_swd_proxy_patched", False):
        return True

    def _new_socket(self) -> None:
        # Real socket is created in _connect after the proxy handshake.
        self.socket = None

    def _connect(self, server_addr: tuple) -> None:
        raw = socks.socksocket(std_socket.AF_INET, std_socket.SOCK_STREAM)
        # Don't hang forever if the proxy is dead / blackholed.
        raw.settimeout(30)
        try:
            raw.connect(server_addr)
        except OSError:
            with contextlib.suppress(Exception):
                raw.close()
            raise
        except Exception as e:
            with contextlib.suppress(Exception):
                raw.close()
            # Connection.connect only catches socket.error (OSError).
            raise OSError(str(e)) from e

        raw.settimeout(None)
        with contextlib.suppress(Exception):
            raw.setblocking(False)

        fd = raw.detach()
        try:
            self.socket = gsocket.socket(gsocket.AF_INET, gsocket.SOCK_STREAM, fileno=fd)
        except Exception:
            with contextlib.suppress(Exception):
                std_socket.socket(fileno=fd).close()
            raise

    TCPConnection._new_socket = _new_socket  # type: ignore[method-assign]
    TCPConnection._connect = _connect  # type: ignore[method-assign]
    TCPConnection._swd_proxy_patched = True  # type: ignore[attr-defined]
    return True


def setup_proxy(url: str) -> ParsedProxy:
    """Parse ``url`` and configure proxy for stdlib + Steam CM + CDN paths.

    * ``pysocks.set_default_proxy`` + ``socket.socket = socks.socksocket``
      (stdlib / anything that uses the socket factory)
    * :func:`patch_steam_cm_tcp` so gevent CM connections also tunnel
    * ``getaddrinfo`` fallback for flaky dual-stack DNS

    Idempotent — calling twice just reconfigures. Returns the
    :class:`ParsedProxy` for inspection / tests.
    """
    import socket

    import socks  # lazy import — pysocks is only needed when proxy is in use

    parsed = parse_proxy_url(url)
    socks.set_default_proxy(
        getattr(socks, parsed.proto_attr),
        parsed.host,
        parsed.port,
        rdns=parsed.rdns,
        username=parsed.username,
        password=parsed.password,
    )
    socket.socket = socks.socksocket  # type: ignore[misc]

    # Patch getaddrinfo to fall back to gethostbyname on failure.
    # This works around networks where getaddrinfo returns error 10044
    # for Steam domains even though the host is resolvable.
    _orig_getaddrinfo = socket.getaddrinfo
    socket.getaddrinfo = _make_getaddrinfo_wrapper(_orig_getaddrinfo)

    # Critical: without this, QueryFiles/GetDetails CM traffic bypasses proxy.
    patch_steam_cm_tcp()
    return parsed


__all__ = [
    "ParsedProxy",
    "parse_proxy_url",
    "patch_steam_cm_tcp",
    "setup_proxy",
]
