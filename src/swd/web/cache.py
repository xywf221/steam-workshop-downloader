"""Small in-process TTL caches for Workshop browse (list pages + items).

Avoids re-hitting Steam UM for back/forward navigation and for detail pages
right after a list view. Not durable; one process only.
"""

from __future__ import annotations

import threading
import time
from collections import OrderedDict
from typing import Any, Generic, TypeVar

from swd.steam.workshop import QueryResult, WorkshopItem

T = TypeVar("T")

# List pages are expensive (QueryFiles); keep a few minutes.
QUERY_TTL_S = 180.0
# Item cards from list are enough for detail for a while.
ITEM_TTL_S = 300.0
MAX_QUERY_ENTRIES = 64
MAX_ITEM_ENTRIES = 512


class _TTLCache(Generic[T]):
    def __init__(self, maxsize: int, ttl: float) -> None:
        self._maxsize = max(1, maxsize)
        self._ttl = ttl
        self._lock = threading.Lock()
        self._data: OrderedDict[Any, tuple[float, T]] = OrderedDict()

    def get(self, key: Any) -> T | None:
        now = time.monotonic()
        with self._lock:
            hit = self._data.get(key)
            if hit is None:
                return None
            expires, value = hit
            if expires <= now:
                del self._data[key]
                return None
            self._data.move_to_end(key)
            return value

    def put(self, key: Any, value: T) -> None:
        now = time.monotonic()
        with self._lock:
            self._data[key] = (now + self._ttl, value)
            self._data.move_to_end(key)
            while len(self._data) > self._maxsize:
                self._data.popitem(last=False)


class BrowseCache:
    """Process-wide caches shared by HTML + JSON routes."""

    def __init__(self) -> None:
        self._queries: _TTLCache[QueryResult] = _TTLCache(MAX_QUERY_ENTRIES, QUERY_TTL_S)
        self._items: _TTLCache[WorkshopItem] = _TTLCache(MAX_ITEM_ENTRIES, ITEM_TTL_S)
        # (app, q, sort, per_page, page) -> next_cursor from that page.
        # Lets page=N+1 without an explicit cursor still use Steam cursors.
        self._page_next: _TTLCache[str] = _TTLCache(MAX_QUERY_ENTRIES * 4, QUERY_TTL_S)

    @staticmethod
    def query_key(
        app_id: int,
        *,
        q: str,
        sort: str,
        page: int,
        per_page: int,
        cursor: str,
    ) -> tuple:
        return (int(app_id), q or "", sort or "", int(page), int(per_page), cursor or "")

    @staticmethod
    def page_chain_key(
        app_id: int,
        *,
        q: str,
        sort: str,
        page: int,
        per_page: int,
    ) -> tuple:
        return (int(app_id), q or "", sort or "", int(page), int(per_page))

    def get_query(self, key: tuple) -> QueryResult | None:
        return self._queries.get(key)

    def put_query(self, key: tuple, result: QueryResult) -> None:
        self._queries.put(key, result)
        for item in result.items:
            self.put_item(item)
        # key = (app, q, sort, page, per_page, cursor)
        app_id, q, sort, page, per_page, _cursor = key
        if result.next_cursor:
            self._page_next.put(
                self.page_chain_key(app_id, q=q, sort=sort, page=page, per_page=per_page),
                result.next_cursor,
            )

    def cursor_for_page(
        self,
        app_id: int,
        *,
        q: str,
        sort: str,
        page: int,
        per_page: int,
        cursor: str,
    ) -> str:
        """Resolve the Steam cursor to use for this page request.

        Explicit ``cursor`` wins. Otherwise, for page>1, reuse the previous
        page's ``next_cursor`` if we still have it cached (HTML "Next" without
        a stale link, or users editing only ``page=``).
        """
        if cursor:
            return cursor
        if page <= 1:
            return ""
        prev = self._page_next.get(
            self.page_chain_key(app_id, q=q, sort=sort, page=page - 1, per_page=per_page)
        )
        return prev or ""

    def get_item(self, item_id: int) -> WorkshopItem | None:
        return self._items.get(int(item_id))

    def put_item(self, item: WorkshopItem) -> None:
        self._items.put(int(item.id), item)


__all__ = ["BrowseCache", "ITEM_TTL_S", "QUERY_TTL_S"]
