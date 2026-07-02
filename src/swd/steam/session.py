"""Steam anonymous login + CDN client construction."""

from __future__ import annotations

from swd.steam.patch import patch_cdn_client_get_chunk
from swd.steam.proxy import setup_proxy
from swd.ui.log import Log


def init_session(proxy_url: str | None, log: Log) -> tuple[object | None, object | None]:
    """Open a Steam anonymous session + CDN client.

    If ``proxy_url`` is ``None``, no proxy is configured. Returns
    ``(client, cdn)`` on success, ``(None, None)`` on login failure.
    """
    if proxy_url is not None:
        setup_proxy(proxy_url)
    patch_cdn_client_get_chunk()

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
    log.ok(f"Server: {cdn.get_content_server()}")
    return client, cdn


__all__ = ["init_session"]
