"""Proxy URL parsing + ``pysocks`` wiring.

Two layers:

- :func:`parse_proxy_url` is a pure function (no I/O, easy to unit-test).
- :func:`setup_proxy` mutates the global ``socket.socket`` to route every
  outbound connection through the parsed proxy.
"""

from __future__ import annotations

import re
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


def setup_proxy(url: str) -> ParsedProxy:
    """Parse ``url`` and configure ``pysocks`` as the default proxy.

    Mutates ``socket.socket`` globally. Idempotent — calling twice just
    reconfigures. Returns the :class:`ParsedProxy` for inspection / tests.
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
    return parsed


__all__ = ["ParsedProxy", "parse_proxy_url", "setup_proxy"]
