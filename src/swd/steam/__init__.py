"""Steam protocol layer: proxy, login, workshop query / collection expansion."""

from swd.steam.patch import (
    apply_steam_patches,
    patch_cdn_client_get_chunk,
    patch_cdn_client_get_manifest,
)
from swd.steam.proxy import ParsedProxy, parse_proxy_url, patch_steam_cm_tcp, setup_proxy
from swd.steam.session import init_session
from swd.steam.workshop import (
    QueryResult,
    WorkshopItem,
    get_item_details,
    query_files,
    resolve_ids,
    resolve_query_type,
)

__all__ = [
    "ParsedProxy",
    "QueryResult",
    "WorkshopItem",
    "apply_steam_patches",
    "get_item_details",
    "init_session",
    "parse_proxy_url",
    "patch_cdn_client_get_chunk",
    "patch_cdn_client_get_manifest",
    "patch_steam_cm_tcp",
    "query_files",
    "resolve_ids",
    "resolve_query_type",
    "setup_proxy",
]
