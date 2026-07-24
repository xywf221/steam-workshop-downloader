"""Server-side Steam media fetch so browser images can ride the app proxy.

When ``swd-web --proxy`` is set, the browser still cannot use that proxy for
``<img src="https://steam...">``. Routes rewrite preview URLs to
``/media/image?u=...`` and this module fetches the bytes (via the same
pysocks-wired sockets as Steam, or an explicit opener).
"""

from __future__ import annotations

import ipaddress
import threading
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import ProxyHandler, Request, build_opener

from swd.steam.proxy import ParsedProxy, parse_proxy_url

# Cap a single preview so a bad/huge URL cannot fill memory.
MAX_MEDIA_BYTES = 8 * 1024 * 1024
DEFAULT_TIMEOUT = 12
# Limit concurrent preview fetches so a page of 20 cards does not stampede
# the SOCKS/HTTP proxy and starve Steam CM (QueryFiles / GetDetails timeouts).
MAX_MEDIA_CONCURRENT = 3
_media_sem = threading.Semaphore(MAX_MEDIA_CONCURRENT)

# Host suffixes Valve uses for Workshop previews / community images.
_ALLOWED_HOST_SUFFIXES = (
    "steamstatic.com",
    "steamusercontent.com",
    "akamaihd.net",
    "steamcommunity.com",
    "steampowered.com",
    "steamserver.net",
)


def is_allowed_media_host(host: str) -> bool:
    """Return True if ``host`` is a known Steam CDN / community host."""
    h = (host or "").strip().lower().rstrip(".")
    if not h or h == "localhost" or h.endswith(".local"):
        return False
    # Reject raw IPs (SSRF: no reach-into-LAN via numeric host).
    try:
        ipaddress.ip_address(h)
        return False
    except ValueError:
        pass
    return any(h == suf or h.endswith("." + suf) for suf in _ALLOWED_HOST_SUFFIXES)


def validate_media_url(url: str) -> str:
    """Normalize and validate a media URL. Raises ``ValueError`` if unsafe."""
    raw = (url or "").strip()
    if not raw:
        raise ValueError("empty url")
    parsed = urlparse(raw)
    if parsed.scheme not in ("http", "https"):
        raise ValueError("url scheme must be http or https")
    if not parsed.netloc or "@" in parsed.netloc:
        raise ValueError("invalid host")
    host = parsed.hostname or ""
    if not is_allowed_media_host(host):
        raise ValueError(f"host not allowed: {host}")
    # Rebuild without fragment; keep query (Steam CDNs sometimes use it).
    cleaned = parsed._replace(fragment="").geturl()
    return cleaned


def _proxy_handler(parsed: ParsedProxy) -> Any | None:
    """Build a urllib ProxyHandler for HTTP CONNECT proxies; None for SOCKS.

    SOCKS is handled by :func:`swd.steam.proxy.setup_proxy` (global socket
    patch). HTTP proxies work better via urllib's ProxyHandler so CONNECT
    auth is applied correctly without relying solely on socksocket.
    """
    if parsed.proto_attr != "HTTP":
        return None
    # urllib expects scheme://[user:pass@]host:port
    auth = ""
    if parsed.username is not None:
        user = parsed.username
        password = parsed.password or ""
        auth = f"{user}:{password}@"
    proxy = f"http://{auth}{parsed.host}:{parsed.port}"
    return ProxyHandler({"http": proxy, "https": proxy})


def fetch_media(
    url: str,
    *,
    proxy_url: str | None = None,
    timeout: float = DEFAULT_TIMEOUT,
    max_bytes: int = MAX_MEDIA_BYTES,
) -> tuple[bytes, str]:
    """Fetch ``url`` and return ``(body, content_type)``.

    If ``proxy_url`` is set, HTTP proxies use urllib ProxyHandler; SOCKS
    proxies assume :func:`setup_proxy` already patched sockets (or we set
    them up here for the process).
    """
    cleaned = validate_media_url(url)

    handlers: list[Any] = []
    if proxy_url:
        parsed = parse_proxy_url(proxy_url)
        if parsed.proto_attr == "HTTP":
            ph = _proxy_handler(parsed)
            if ph is not None:
                handlers.append(ph)
        else:
            # Ensure SOCKS is wired for this process (idempotent).
            from swd.steam.proxy import setup_proxy

            setup_proxy(proxy_url)

    opener = build_opener(*handlers)
    req = Request(
        cleaned,
        headers={
            "User-Agent": "swd-web-media/1.0",
            "Accept": "image/*,*/*;q=0.8",
        },
        method="GET",
    )
    with _media_sem:
        try:
            with opener.open(req, timeout=timeout) as resp:
                ctype = resp.headers.get_content_type() or "application/octet-stream"
                # Prefer image/*; still allow octet-stream (some CDNs omit type).
                allowed = ctype.startswith("image/") or ctype in (
                    "application/octet-stream",
                    "binary/octet-stream",
                )
                if not allowed:
                    raise ValueError(f"unexpected content-type: {ctype}")
                chunks: list[bytes] = []
                total = 0
                while True:
                    block = resp.read(64 * 1024)
                    if not block:
                        break
                    total += len(block)
                    if total > max_bytes:
                        raise ValueError(f"media exceeds {max_bytes} bytes")
                    chunks.append(block)
                return b"".join(chunks), ctype
        except HTTPError as e:
            raise RuntimeError(f"upstream HTTP {e.code}") from e
        except (URLError, TimeoutError, OSError) as e:
            raise RuntimeError(f"fetch failed: {e}") from e


def media_proxy_path(original_url: str) -> str:
    """Relative path for the Flask media route (caller urlencodes query)."""
    from urllib.parse import quote

    return f"/media/image?u={quote(original_url, safe='')}"


def display_preview_url(original_url: str, *, proxy_url: str | None) -> str:
    """URL to put in ``<img src>``: proxied when a proxy is configured."""
    if not original_url:
        return ""
    if not proxy_url:
        return original_url
    try:
        validate_media_url(original_url)
    except ValueError:
        # Unknown host — leave as-is rather than 400 the whole page.
        return original_url
    return media_proxy_path(original_url)


__all__ = [
    "MAX_MEDIA_BYTES",
    "display_preview_url",
    "fetch_media",
    "is_allowed_media_host",
    "media_proxy_path",
    "validate_media_url",
]
