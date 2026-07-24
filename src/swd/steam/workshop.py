"""Workshop discovery and collection expansion via Steam UM APIs.

* :func:`resolve_ids` — expand collections (``file_type == 2``) for downloads.
* :func:`query_files` — browse / search / paginate a game's Workshop.
* :func:`get_item_details` — single-item detail via GetDetails.
"""

from __future__ import annotations

import time
from collections.abc import Iterable
from dataclasses import dataclass, field
from typing import Any

from swd.constants import PUBLISHED_FILE_BATCH, UM_RETRIES, UM_TIMEOUT
from swd.ui.log import Log
from swd.utils import compute_backoff

# EPublishedFileQueryType values used by the web UI / query_files.
QUERY_TYPE_RANKED_BY_VOTE = 0
QUERY_TYPE_RANKED_BY_PUBLICATION_DATE = 1
QUERY_TYPE_RANKED_BY_TREND = 3
QUERY_TYPE_RANKED_BY_TOTAL_UNIQUE_SUBSCRIPTIONS = 9
QUERY_TYPE_RANKED_BY_TEXT_SEARCH = 12

SORT_TO_QUERY_TYPE: dict[str, int] = {
    "votes": QUERY_TYPE_RANKED_BY_VOTE,
    "new": QUERY_TYPE_RANKED_BY_PUBLICATION_DATE,
    "trend": QUERY_TYPE_RANKED_BY_TREND,
    "subscriptions": QUERY_TYPE_RANKED_BY_TOTAL_UNIQUE_SUBSCRIPTIONS,
    "search": QUERY_TYPE_RANKED_BY_TEXT_SEARCH,
}


@dataclass(frozen=True)
class WorkshopItem:
    """Plain representation of one published file for API / UI use."""

    id: int
    title: str
    short_description: str = ""
    preview_url: str = ""
    file_size: int = 0
    subscriptions: int = 0
    favorited: int = 0
    views: int = 0
    time_created: int = 0
    time_updated: int = 0
    file_type: int = 0
    creator: int = 0
    consumer_appid: int = 0
    tags: list[str] = field(default_factory=list)
    vote_score: float | None = None
    banned: bool = False


@dataclass(frozen=True)
class QueryResult:
    """Result of a :func:`query_files` call."""

    total: int
    items: list[WorkshopItem]
    page: int
    numperpage: int
    next_cursor: str = ""


def _ensure_session(client: Any, log: Log, attempt: int) -> Exception | None:
    """Reconnect + anonymous login if the client is dead. Returns last error or None."""
    try:
        connected = bool(getattr(client, "connected", False))
        logged_on = bool(getattr(client, "logged_on", False))
    except Exception:
        connected = logged_on = False
    if connected and logged_on:
        return None
    try:
        if not connected:
            client.reconnect(maxdelay=10, retry=3)
        if not getattr(client, "logged_on", False):
            client.anonymous_login()
        return None
    except Exception as e:
        if attempt < UM_RETRIES:
            wait = compute_backoff(attempt)
            log.warn(
                f"Steam session dead, reconnect failed "
                f"({attempt}/{UM_RETRIES}): {e}; retry in {wait:.0f}s"
            )
            time.sleep(wait)
        return e


def _send_um(client: Any, method: str, body: dict[str, Any], log: Log) -> Any:
    """Call ``client.send_um_and_wait`` with session heal + retries; never returns None."""
    last_err: Exception | None = None
    for attempt in range(1, UM_RETRIES + 1):
        heal_err = _ensure_session(client, log, attempt)
        if heal_err is not None:
            last_err = heal_err
            if attempt >= UM_RETRIES:
                break
            continue

        resp = client.send_um_and_wait(method, body, timeout=UM_TIMEOUT)
        if resp is not None:
            return resp

        last_err = TimeoutError(
            f"{method} timed out (attempt {attempt}/{UM_RETRIES}, timeout={UM_TIMEOUT}s)"
        )
        if attempt < UM_RETRIES:
            wait = compute_backoff(attempt)
            log.warn(f"{last_err}; retry in {wait:.0f}s")
            time.sleep(wait)

    raise last_err if last_err is not None else RuntimeError(f"{method} failed")


def _send_details(client: Any, batch: list[int], log: Log) -> Any:
    """Call PublishedFile.GetDetails with retries; never returns None."""
    return _send_um(
        client,
        "PublishedFile.GetDetails#1",
        {
            "publishedfileids": batch,
            "includetags": False,
            "includeadditionalpreviews": False,
            "includechildren": True,
            "includekvtags": False,
            "includevotes": False,
            "short_description": True,
            "includeforsaledata": False,
            "includemetadata": False,
            "language": 0,
        },
        log,
    )


def _tag_names(wf: Any) -> list[str]:
    tags = getattr(wf, "tags", None) or []
    out: list[str] = []
    for t in tags:
        name = getattr(t, "tag", None) or getattr(t, "display_name", None) or ""
        if name:
            out.append(str(name))
    return out


def _vote_score(wf: Any) -> float | None:
    vd = getattr(wf, "vote_data", None)
    if vd is None:
        return None
    score = getattr(vd, "score", None)
    if score is None:
        return None
    try:
        return float(score)
    except (TypeError, ValueError):
        return None


def workshop_item_from_details(wf: Any) -> WorkshopItem | None:
    """Map a GetDetails / QueryFiles ``publishedfiledetails`` entry to :class:`WorkshopItem`.

    Returns ``None`` when Steam reports a non-OK result.
    """
    from steam.enums import EResult

    result = getattr(wf, "result", None)
    # QueryFiles sometimes omits result or sets 0; treat missing/0 as OK.
    if result not in (None, 0) and result != EResult.OK:
        return None

    pid = int(getattr(wf, "publishedfileid", 0) or 0)
    if pid == 0:
        return None

    return WorkshopItem(
        id=pid,
        title=str(getattr(wf, "title", None) or pid),
        short_description=str(
            getattr(wf, "short_description", None)
            or getattr(wf, "file_description", None)
            or ""
        ),
        preview_url=str(getattr(wf, "preview_url", None) or getattr(wf, "image_url", None) or ""),
        file_size=int(getattr(wf, "file_size", 0) or 0),
        subscriptions=int(
            getattr(wf, "subscriptions", 0)
            or getattr(wf, "lifetime_subscriptions", 0)
            or 0
        ),
        favorited=int(getattr(wf, "favorited", 0) or getattr(wf, "lifetime_favorited", 0) or 0),
        views=int(getattr(wf, "views", 0) or 0),
        time_created=int(getattr(wf, "time_created", 0) or 0),
        time_updated=int(getattr(wf, "time_updated", 0) or 0),
        file_type=int(getattr(wf, "file_type", 0) or 0),
        creator=int(getattr(wf, "creator", 0) or 0),
        consumer_appid=int(getattr(wf, "consumer_appid", 0) or 0),
        tags=_tag_names(wf),
        vote_score=_vote_score(wf),
        banned=bool(getattr(wf, "banned", False)),
    )


def resolve_ids(client: Any, app_id: int, ids: Iterable[int], log: Log) -> list[int]:
    """Resolve ``ids`` against the Steam API, expanding collections."""
    from steam.enums import EResult

    all_ids = list(dict.fromkeys(ids))
    resolved: list[int] = []

    for start in range(0, len(all_ids), PUBLISHED_FILE_BATCH):
        batch = all_ids[start : start + PUBLISHED_FILE_BATCH]
        try:
            resp = _send_details(client, batch, log)
        except Exception as e:
            log.err(f"Failed to resolve batch {batch}: {e}")
            continue

        if not getattr(resp, "body", None) or not resp.body.publishedfiledetails:
            log.warn(f"empty GetDetails response for batch {batch}, skipping")
            continue

        for wf in resp.body.publishedfiledetails:
            if wf.result != EResult.OK:
                log.warn(f"failed to get details for {wf.publishedfileid}, skipping")
                continue

            title = wf.title or str(wf.publishedfileid)
            if wf.file_type == 2:  # Collection
                child_ids = [c.publishedfileid for c in wf.children]
                log.info(f"  '{title}' is a collection, {len(child_ids)} items")
                resolved.extend(resolve_ids(client, app_id, child_ids, log))
            else:
                resolved.append(wf.publishedfileid)

    return resolved


def resolve_query_type(sort: str | None, search_text: str) -> int:
    """Map a UI ``sort`` name + optional search text to ``EPublishedFileQueryType``."""
    key = (sort or "").strip().lower()
    if key in SORT_TO_QUERY_TYPE:
        return SORT_TO_QUERY_TYPE[key]
    if search_text.strip():
        return QUERY_TYPE_RANKED_BY_TEXT_SEARCH
    return QUERY_TYPE_RANKED_BY_TREND


# Steam cursor token for the first page of QueryFiles. Page-number pagination
# (page=2,3,...) walks the full result set server-side and routinely times out
# over high-latency / proxied links; cursor pagination stays O(page size).
QUERY_CURSOR_START = "*"


def query_files(
    client: Any,
    app_id: int,
    *,
    search_text: str = "",
    page: int = 1,
    numperpage: int = 20,
    query_type: int | None = None,
    cursor: str | None = None,
    log: Log,
) -> QueryResult:
    """Browse / search Workshop items for ``app_id`` via PublishedFile.QueryFiles.

    Prefer **cursor** pagination: pass ``cursor="*"`` (or omit / empty for page 1
    and we set it) then feed back ``result.next_cursor``. Numeric ``page`` alone
    is only a fallback and is slow for page > 1.
    """
    if page < 1:
        page = 1
    if numperpage < 1:
        numperpage = 1

    if query_type is None:
        query_type = resolve_query_type(None, search_text)

    # Normalize cursor: first page always starts at "*".
    use_cursor = (cursor or "").strip()
    if not use_cursor:
        use_cursor = QUERY_CURSOR_START if page <= 1 else ""

    body: dict[str, Any] = {
        "query_type": int(query_type),
        # With a cursor Steam ignores page; keep page=1 to avoid deep walks.
        "page": 1 if use_cursor else int(page),
        "numperpage": int(numperpage),
        "appid": int(app_id),
        "search_text": search_text or "",
        "return_vote_data": True,
        "return_tags": True,
        "return_previews": True,
        "return_short_description": True,
        # return_details is heavier and not needed for cards / list UI.
        "return_details": False,
        "strip_description_bbcode": True,
        "language": 0,
        "cache_max_age_seconds": 300,
    }
    if use_cursor:
        body["cursor"] = use_cursor

    resp = _send_um(client, "PublishedFile.QueryFiles#1", body, log)
    body_msg = getattr(resp, "body", None)
    if body_msg is None:
        return QueryResult(total=0, items=[], page=page, numperpage=numperpage)

    total = int(getattr(body_msg, "total", 0) or 0)
    next_cursor = str(getattr(body_msg, "next_cursor", None) or "")
    items: list[WorkshopItem] = []
    for wf in getattr(body_msg, "publishedfiledetails", None) or []:
        item = workshop_item_from_details(wf)
        if item is not None:
            items.append(item)

    return QueryResult(
        total=total,
        items=items,
        page=page,
        numperpage=numperpage,
        next_cursor=next_cursor,
    )


def get_item_details(client: Any, publishedfileid: int, *, log: Log) -> WorkshopItem | None:
    """Fetch a single Workshop item via PublishedFile.GetDetails."""
    resp = _send_um(
        client,
        "PublishedFile.GetDetails#1",
        {
            "publishedfileids": [int(publishedfileid)],
            "includetags": True,
            "includeadditionalpreviews": False,
            "includechildren": False,
            "includekvtags": False,
            "includevotes": True,
            "short_description": True,
            "includeforsaledata": False,
            "includemetadata": False,
            "language": 0,
        },
        log,
    )
    body = getattr(resp, "body", None)
    details = getattr(body, "publishedfiledetails", None) if body is not None else None
    if not details:
        return None
    return workshop_item_from_details(details[0])


__all__ = [
    "QUERY_CURSOR_START",
    "QUERY_TYPE_RANKED_BY_PUBLICATION_DATE",
    "QUERY_TYPE_RANKED_BY_TEXT_SEARCH",
    "QUERY_TYPE_RANKED_BY_TOTAL_UNIQUE_SUBSCRIPTIONS",
    "QUERY_TYPE_RANKED_BY_TREND",
    "QUERY_TYPE_RANKED_BY_VOTE",
    "QueryResult",
    "SORT_TO_QUERY_TYPE",
    "WorkshopItem",
    "get_item_details",
    "query_files",
    "resolve_ids",
    "resolve_query_type",
    "workshop_item_from_details",
]
