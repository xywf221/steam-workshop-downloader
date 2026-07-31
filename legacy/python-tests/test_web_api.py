"""Flask JSON API tests (Steam fully mocked)."""

from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from swd.steam.workshop import QueryResult, WorkshopItem
from swd.ui import Log

flask = pytest.importorskip("flask")


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def _item(pid: int = 1001, title: str = "Mod") -> WorkshopItem:
    return WorkshopItem(
        id=pid,
        title=title,
        short_description="hello",
        preview_url="http://example/p.jpg",
        file_size=2048,
        subscriptions=5,
        favorited=1,
        views=10,
        time_created=1,
        time_updated=2,
        file_type=0,
        creator=9,
        consumer_appid=294100,
        tags=["Mod"],
        vote_score=0.8,
        banned=False,
    )


@pytest.fixture
def client(log: Log):
    from swd.web.app import create_app

    steam = MagicMock()
    steam.logged_on = False
    steam.has_client = False
    steam.warmup = MagicMock()

    def _call(fn, *args, **kwargs):
        # Simulate SteamSession.call(fn, client, ...):
        # SteamSession.call does: fn(client, *args, **kwargs)
        raise AssertionError(f"unexpected steam.call: {fn!r}")

    steam.call.side_effect = _call
    app = create_app(log=log, steam=steam)
    app.config["TESTING"] = True
    with app.test_client() as c:
        c._steam = steam  # type: ignore[attr-defined]
        yield c


def test_health(client) -> None:
    rv = client.get("/api/health")
    assert rv.status_code == 200
    data = rv.get_json()
    assert data["ok"] is True
    assert "steam_logged_on" in data


def test_workshop_list_missing_appid(client) -> None:
    rv = client.get("/api/workshop")
    assert rv.status_code == 400


def test_workshop_list_ok(client, log: Log) -> None:
    from swd.steam.workshop import query_files

    result = QueryResult(total=2, items=[_item(1, "A"), _item(2, "B")], page=1, numperpage=20)

    def _call(fn, *args, **kwargs):
        assert fn is query_files
        assert args[0] == 294100
        assert kwargs["search_text"] == "combat"
        assert kwargs["page"] == 1
        return result

    client._steam.call.side_effect = _call
    rv = client.get("/api/workshop?appid=294100&q=combat&page=1")
    assert rv.status_code == 200
    data = rv.get_json()
    assert data["total"] == 2
    assert data["page"] == 1
    assert data["per_page"] == 20
    assert len(data["items"]) == 2
    assert data["items"][0]["id"] == 1
    assert data["items"][0]["title"] == "A"
    assert data["items"][0]["steam_url"].endswith("id=1")
    # Second identical request should be served from BrowseCache (no Steam call).
    client._steam.call.side_effect = AssertionError("should use cache")
    rv2 = client.get("/api/workshop?appid=294100&q=combat&page=1")
    assert rv2.status_code == 200
    assert rv2.get_json()["total"] == 2


def test_workshop_detail_ok(client) -> None:
    from swd.steam.workshop import get_item_details

    item = _item(55, "Detail")

    def _call(fn, *args, **kwargs):
        assert fn is get_item_details
        assert args[0] == 55
        return item

    client._steam.call.side_effect = _call
    rv = client.get("/api/workshop/55")
    assert rv.status_code == 200
    data = rv.get_json()
    assert data["id"] == 55
    assert data["title"] == "Detail"
    assert data["tags"] == ["Mod"]
    # Cached detail — no second Steam round-trip.
    client._steam.call.side_effect = AssertionError("should use item cache")
    rv2 = client.get("/api/workshop/55")
    assert rv2.status_code == 200
    assert rv2.get_json()["id"] == 55


def test_workshop_detail_from_list_cache(client) -> None:
    """Items seen in a list response should serve detail without GetDetails."""
    from swd.steam.workshop import query_files

    result = QueryResult(total=1, items=[_item(77, "FromList")], page=1, numperpage=20)

    def _call(fn, *args, **kwargs):
        assert fn is query_files
        return result

    client._steam.call.side_effect = _call
    assert client.get("/api/workshop?appid=294100").status_code == 200
    client._steam.call.side_effect = AssertionError("detail should hit list cache")
    rv = client.get("/api/workshop/77")
    assert rv.status_code == 200
    assert rv.get_json()["title"] == "FromList"


def test_workshop_detail_not_found(client) -> None:
    from swd.steam.workshop import get_item_details

    def _call(fn, *args, **kwargs):
        assert fn is get_item_details
        return None

    client._steam.call.side_effect = _call
    rv = client.get("/api/workshop/999")
    assert rv.status_code == 404


def test_index_html_without_appid(client) -> None:
    rv = client.get("/")
    assert rv.status_code == 200
    assert b"AppID" in rv.data


def test_index_html_with_results(client) -> None:
    from swd.steam.workshop import query_files

    result = QueryResult(
        total=40,
        items=[_item(7, "Combat AI")],
        page=1,
        numperpage=20,
        next_cursor="CUR2",
    )

    def _call(fn, *args, **kwargs):
        assert fn is query_files
        return result

    client._steam.call.side_effect = _call
    rv = client.get("/?appid=294100&q=combat")
    assert rv.status_code == 200
    assert b"Combat AI" in rv.data
    assert b"cursor=CUR2" in rv.data


def test_page2_without_cursor_uses_chain(client) -> None:
    """After page1, page=2 without cursor should reuse cached next_cursor."""
    from swd.steam.workshop import query_files

    page1 = QueryResult(
        total=40,
        items=[_item(1, "A")],
        page=1,
        numperpage=20,
        next_cursor="CHAIN2",
    )
    page2 = QueryResult(
        total=40,
        items=[_item(2, "B")],
        page=2,
        numperpage=20,
        next_cursor="CHAIN3",
    )
    calls: list[dict] = []

    def _call(fn, *args, **kwargs):
        assert fn is query_files
        calls.append(kwargs)
        if len(calls) == 1:
            return page1
        return page2

    client._steam.call.side_effect = _call
    assert client.get("/api/workshop?appid=294100&sort=trend&page=1").status_code == 200
    rv = client.get("/api/workshop?appid=294100&sort=trend&page=2")
    assert rv.status_code == 200
    assert rv.get_json()["items"][0]["id"] == 2
    assert calls[1].get("cursor") == "CHAIN2"
