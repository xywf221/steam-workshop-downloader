"""Steam anonymous login + CDN client construction."""

from __future__ import annotations

from swd.steam.patch import apply_steam_patches
from swd.steam.proxy import parse_proxy_url, setup_proxy
from swd.ui.log import Log

# anonymous_login is a single CM attempt; channel encrypt often needs a few
# TryAnotherCM rotations when the exit path is flaky or filters CM peers.
_ANON_LOGIN_ATTEMPTS = 5


def _eresult_label(value: object) -> str:
    """Best-effort ``EResult`` name for logs (never raises)."""
    try:
        from steam.enums import EResult

        er = EResult(value)
        name = getattr(er, "name", None)
        if name:
            return f"{int(er)} {name}"
        return str(int(er))
    except Exception:
        return repr(value)


def init_session(proxy_url: str | None, log: Log) -> tuple[object | None, object | None]:
    """Open a Steam anonymous session + CDN client.

    If ``proxy_url`` is ``None``, no proxy is configured. Returns
    ``(client, cdn)`` on success, ``(None, None)`` on login failure.
    """
    if proxy_url is not None:
        # Also patches Steam CM TCP (gevent) — without that, QueryFiles /
        # GetDetails would still go direct while only CDN HTTP used the proxy.
        parsed = setup_proxy(proxy_url)
        auth = "auth=yes" if parsed.username else "auth=no"
        log.dim(f"  Proxy wired for CM + CDN + media: {parsed.redacted_url()} ({auth})")
    apply_steam_patches()

    from steam.client import SteamClient
    from steam.client.cdn import CDNClient
    from steam.enums import EResult

    log.stage("INIT", "Connecting to Steam...")
    client = SteamClient()
    last: object | None = None
    for attempt in range(1, _ANON_LOGIN_ATTEMPTS + 1):
        result = client.anonymous_login()
        last = result
        if result == EResult.OK:
            break
        label = _eresult_label(result)
        if attempt < _ANON_LOGIN_ATTEMPTS and result in (
            EResult.TryAnotherCM,
            EResult.Fail,
            EResult.Timeout,
            EResult.ServiceUnavailable,
        ):
            log.warn(
                f"Anonymous login attempt {attempt}/{_ANON_LOGIN_ATTEMPTS} "
                f"failed ({label}); trying another CM..."
            )
            try:
                if getattr(client, "connected", False):
                    client.disconnect()
            except Exception:
                pass
            continue
        break
    else:
        result = last

    if result != EResult.OK:
        log.err(f"Login failed ({_eresult_label(result)})")
        if proxy_url is not None:
            # Common failure mode with "web-only" SOCKS exits: TCP CONNECT to
            # CM :27017 succeeds (or appears to), then the reader sees EOF
            # before ChannelEncryptRequest — steam reports TryAnotherCM.
            try:
                p = parse_proxy_url(proxy_url)
                hint_host = p.host
            except Exception:
                hint_host = "proxy"
            log.dim(
                "  Hint: credentials in --proxy are parsed; this usually means "
                f"the exit ({hint_host}) cannot complete Steam CM on TCP 27017 "
                "(web-only / port-filtered proxies often break here). "
                "Use a full-cone SOCKS that allows arbitrary TCP, not HTTPS-only."
            )
        return None, None
    log.ok(f"Logged on ({client.steam_id})")

    log.stage("INIT", "Getting content servers...")
    cdn = CDNClient(client)
    server = cdn.get_content_server()
    log.ok(f"Server: {server}")
    # cell_id=0 is Valve's "no region matched" fallback. Depot chunks on
    # those PoPs are routinely 403'd for anonymous sessions (especially
    # from CN direct exits that land on clngaa.com). Surface a clear
    # hint instead of letting the user discover it via opaque HTTP 403s.
    cell_id = getattr(server, "cell_id", None)
    if cell_id == 0:
        log.warn(
            "Content server has cell_id=0 (no region matched). "
            "Anonymous depot downloads from this PoP often fail with HTTP 403. "
            "Pass --proxy with an exit outside mainland China (e.g. HK/JP/SG/US) "
            "so Steam assigns a real cell."
        )
    return client, cdn


__all__ = ["init_session"]
