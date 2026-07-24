"""JSON-friendly dicts for :class:`~swd.steam.workshop.WorkshopItem`."""

from __future__ import annotations

from typing import Any

from swd.steam.workshop import QueryResult, WorkshopItem
from swd.utils import fmt_size
from swd.web.media import display_preview_url


def item_to_dict(item: WorkshopItem, *, proxy_url: str | None = None) -> dict[str, Any]:
    """Serialize an item.

    ``preview_url`` is always the original Steam CDN URL.
    ``preview_src`` is what the HTML UI should load — rewritten to
    ``/media/image?u=...`` when ``proxy_url`` is set so the browser never
    hits Steam CDNs directly.
    """
    preview = item.preview_url or ""
    return {
        "id": item.id,
        "title": item.title,
        "short_description": item.short_description,
        "preview_url": preview,
        "preview_src": display_preview_url(preview, proxy_url=proxy_url),
        "file_size": item.file_size,
        "file_size_human": fmt_size(item.file_size) if item.file_size else "",
        "subscriptions": item.subscriptions,
        "favorited": item.favorited,
        "views": item.views,
        "time_created": item.time_created,
        "time_updated": item.time_updated,
        "file_type": item.file_type,
        "is_collection": item.file_type == 2,
        "creator": item.creator,
        "consumer_appid": item.consumer_appid,
        "tags": list(item.tags),
        "vote_score": item.vote_score,
        "banned": item.banned,
        "steam_url": f"https://steamcommunity.com/sharedfiles/filedetails/?id={item.id}",
    }


def query_to_dict(result: QueryResult, *, proxy_url: str | None = None) -> dict[str, Any]:
    return {
        "total": result.total,
        "page": result.page,
        "per_page": result.numperpage,
        "next_cursor": result.next_cursor,
        "items": [item_to_dict(i, proxy_url=proxy_url) for i in result.items],
    }


__all__ = ["item_to_dict", "query_to_dict"]
