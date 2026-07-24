"""Steam anonymous login + CDN client construction."""

from __future__ import annotations

from swd.steam.patch import apply_steam_patches
from swd.steam.proxy import setup_proxy
from swd.ui.log import Log


def init_session(proxy_url: str | None, log: Log) -> tuple[object | None, object | None]:
    """Open a Steam anonymous session + CDN client.

    If ``proxy_url`` is ``None``, no proxy is configured. Returns
    ``(client, cdn)`` on success, ``(None, None)`` on login failure.
    """
    if proxy_url is not None:
        # Also patches Steam CM TCP (gevent) — without that, QueryFiles /
        # GetDetails would still go direct while only CDN HTTP used the proxy.
        setup_proxy(proxy_url)
        log.dim(f"  Proxy wired for CM + CDN + media: {proxy_url}")
    apply_steam_patches()

    from steam.client import SteamClient
    from steam.client.cdn import CDNClient

    log.stage("INIT", "Connecting to Steam...")
    client = SteamClient()
    if client.anonymous_login() != 1:
        log.err("Login failed")
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
