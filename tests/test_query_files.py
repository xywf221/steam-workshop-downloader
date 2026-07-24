"""Tests for Workshop QueryFiles / GetDetails mapping helpers."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest

from swd.constants import UM_RETRIES
from swd.steam.workshop import (
    QUERY_TYPE_RANKED_BY_TEXT_SEARCH,
    QUERY_TYPE_RANKED_BY_TREND,
    QUERY_TYPE_RANKED_BY_VOTE,
    get_item_details,
    query_files,
    resolve_query_type,
    workshop_item_from_details,
)
from swd.ui import Log


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def _wf(
    *,
    pid: int = 1001,
    result: int = 1,
    title: str = "Mod",
    short_description: str = "desc",
    preview_url: str = "http://example/preview.jpg",
    file_size: int = 1024,
    subscriptions: int = 10,
    favorited: int = 2,
    views: int = 99,
    file_type: int = 0,
    tags=None,
    score: float | None = 0.85,
):
    vote_data = SimpleNamespace(score=score) if score is not None else None
    tag_objs = [SimpleNamespace(tag=t, display_name=t, adminonly=False) for t in (tags or [])]
    return SimpleNamespace(
        publishedfileid=pid,
        result=result,
        title=title,
        short_description=short_description,
        file_description="",
        preview_url=preview_url,
        image_url="",
        file_size=file_size,
        subscriptions=subscriptions,
        lifetime_subscriptions=0,
        favorited=favorited,
        lifetime_favorited=0,
        views=views,
        time_created=1,
        time_updated=2,
        file_type=file_type,
        creator=42,
        consumer_appid=294100,
        tags=tag_objs,
        vote_data=vote_data,
        banned=False,
    )


def test_resolve_query_type_defaults() -> None:
    assert resolve_query_type(None, "") == QUERY_TYPE_RANKED_BY_TREND
    assert resolve_query_type(None, "combat") == QUERY_TYPE_RANKED_BY_TEXT_SEARCH
    assert resolve_query_type("votes", "") == QUERY_TYPE_RANKED_BY_VOTE
    assert resolve_query_type("SEARCH", "x") == QUERY_TYPE_RANKED_BY_TEXT_SEARCH


def test_workshop_item_from_details_maps_fields() -> None:
    item = workshop_item_from_details(_wf(tags=["Mod", "Interface"], score=0.9))
    assert item is not None
    assert item.id == 1001
    assert item.title == "Mod"
    assert item.tags == ["Mod", "Interface"]
    assert item.vote_score == pytest.approx(0.9)
    assert item.consumer_appid == 294100


def test_workshop_item_skips_failed_result() -> None:
    assert workshop_item_from_details(_wf(result=9)) is None


def test_query_files_success(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    client.send_um_and_wait.return_value = SimpleNamespace(
        body=SimpleNamespace(
            total=123,
            next_cursor="abc",
            publishedfiledetails=[_wf(pid=1), _wf(pid=2, title="B")],
        )
    )

    result = query_files(
        client,
        294100,
        search_text="combat",
        page=2,
        numperpage=10,
        cursor="cursor-page-2",
        log=log,
    )

    assert result.total == 123
    assert result.page == 2
    assert result.numperpage == 10
    assert result.next_cursor == "abc"
    assert [i.id for i in result.items] == [1, 2]

    method, body = client.send_um_and_wait.call_args.args[:2]
    assert method == "PublishedFile.QueryFiles#1"
    assert body["appid"] == 294100
    assert body["search_text"] == "combat"
    # Cursor pagination: Steam page stays 1; cursor carries offset.
    assert body["page"] == 1
    assert body["cursor"] == "cursor-page-2"
    assert body["numperpage"] == 10
    assert body["query_type"] == QUERY_TYPE_RANKED_BY_TEXT_SEARCH
    assert body["return_tags"] is True
    assert body.get("return_details") is False


def test_query_files_default_trend_without_search(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    client.send_um_and_wait.return_value = SimpleNamespace(
        body=SimpleNamespace(total=0, next_cursor="", publishedfiledetails=[])
    )

    query_files(client, 730, page=1, numperpage=20, log=log)
    body = client.send_um_and_wait.call_args.args[1]
    assert body["query_type"] == QUERY_TYPE_RANKED_BY_TREND
    assert body["cursor"] == "*"
    assert body["page"] == 1


def test_query_files_retries_on_timeout(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    ok = SimpleNamespace(
        body=SimpleNamespace(total=1, next_cursor="", publishedfiledetails=[_wf()])
    )
    client.send_um_and_wait.side_effect = [None, ok]

    with patch("swd.steam.workshop.time.sleep", return_value=None):
        result = query_files(client, 294100, log=log)

    assert len(result.items) == 1
    assert client.send_um_and_wait.call_count == 2


def test_query_files_raises_after_all_timeouts(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    client.send_um_and_wait.return_value = None

    with (
        patch("swd.steam.workshop.time.sleep", return_value=None),
        pytest.raises(TimeoutError),
    ):
        query_files(client, 294100, log=log)

    assert client.send_um_and_wait.call_count == UM_RETRIES


def test_get_item_details(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    client.send_um_and_wait.return_value = SimpleNamespace(
        body=SimpleNamespace(publishedfiledetails=[_wf(pid=55, title="Detail")])
    )

    item = get_item_details(client, 55, log=log)
    assert item is not None
    assert item.id == 55
    assert item.title == "Detail"
    method = client.send_um_and_wait.call_args.args[0]
    assert method == "PublishedFile.GetDetails#1"
