"""Proxy URL parsing + ``pysocks`` wiring.

Two layers:

- :func:`parse_proxy_url` is a pure function (no I/O, easy to unit-test).
- :func:`setup_proxy` mutates the global ``socket.socket`` to route every
  outbound connection through the parsed proxy.
"""

from __future__ import annotations

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


def setup_proxy(url: str) -> ParsedProxy:
    """Parse ``url`` and configure ``pysocks`` as the default proxy.

    Mutates ``socket.socket`` and ``socket.getaddrinfo`` globally.
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
    return parsed


__all__ = ["ParsedProxy", "parse_proxy_url", "setup_proxy"]
