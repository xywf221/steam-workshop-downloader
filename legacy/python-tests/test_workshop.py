"""Tests for collection expansion + GetDetails retry logic."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest

from swd.constants import UM_RETRIES
from swd.steam.workshop import resolve_ids
from swd.ui import Log


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def _wf(*, pid: int, result: int = 1, file_type: int = 0, title: str = "", children=None):
    return SimpleNamespace(
        publishedfileid=pid,
        result=result,
        file_type=file_type,
        title=title or str(pid),
        children=children or [],
    )


def test_resolve_ids_retries_on_none_then_ok(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    ok = SimpleNamespace(body=SimpleNamespace(publishedfiledetails=[_wf(pid=1001)]))
    client.send_um_and_wait.side_effect = [None, ok]

    with patch("swd.steam.workshop.time.sleep", return_value=None):
        result = resolve_ids(client, 294100, [1001], log)

    assert result == [1001]
    assert client.send_um_and_wait.call_count == 2


def test_resolve_ids_skips_batch_after_all_timeouts(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True
    client.send_um_and_wait.return_value = None

    with patch("swd.steam.workshop.time.sleep", return_value=None):
        result = resolve_ids(client, 294100, [1001, 1002], log)

    assert result == []
    assert client.send_um_and_wait.call_count == UM_RETRIES


def test_resolve_ids_expands_collection(log: Log) -> None:
    client = MagicMock()
    client.connected = True
    client.logged_on = True

    collection = _wf(
        pid=2000,
        file_type=2,
        title="Pack",
        children=[SimpleNamespace(publishedfileid=1001), SimpleNamespace(publishedfileid=1002)],
    )
    child1 = _wf(pid=1001, title="A")
    child2 = _wf(pid=1002, title="B")

    def _side_effect(method, params, timeout=10):
        ids = params["publishedfileids"]
        if ids == [2000]:
            return SimpleNamespace(body=SimpleNamespace(publishedfiledetails=[collection]))
        if set(ids) == {1001, 1002}:
            return SimpleNamespace(body=SimpleNamespace(publishedfiledetails=[child1, child2]))
        # recursive resolve may request one-at-a-time depending on order
        details = []
        for i in ids:
            if i == 1001:
                details.append(child1)
            elif i == 1002:
                details.append(child2)
        return SimpleNamespace(body=SimpleNamespace(publishedfiledetails=details))

    client.send_um_and_wait.side_effect = _side_effect
    result = resolve_ids(client, 294100, [2000], log)
    assert result == [1001, 1002]
