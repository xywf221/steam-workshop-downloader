"""Tests for the hardened get_manifest_for_workshop_item patch."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest

from swd.constants import UM_RETRIES


def _make_ok_resp(*, title: str = "Mod", hcontent: int = 999, consumer: int = 294100):
    from steam.enums import EResult

    header = SimpleNamespace(eresult=EResult.OK, error_message="")
    wf = SimpleNamespace(
        result=EResult.OK,
        hcontent_file=hcontent,
        consumer_appid=consumer,
        title=title,
    )
    body = SimpleNamespace(publishedfiledetails=[wf])
    return SimpleNamespace(header=header, body=body)


@pytest.fixture
def patched_method():
    """Apply the manifest patch, then restore the original on teardown."""
    from steam.client.cdn import CDNClient

    original = CDNClient.get_manifest_for_workshop_item
    with patch("swd.steam.patch.time.sleep", return_value=None):
        from swd.steam.patch import patch_cdn_client_get_manifest

        fn = patch_cdn_client_get_manifest()
        try:
            yield fn
        finally:
            CDNClient.get_manifest_for_workshop_item = original


def test_manifest_retries_on_none_then_succeeds(patched_method) -> None:
    from steam.client.cdn import CDNClient

    steam = MagicMock()
    steam.connected = True
    steam.logged_on = True
    ok = _make_ok_resp(title="Hello")
    steam.send_um_and_wait.side_effect = [None, None, ok]

    self = MagicMock()
    self.steam = steam
    self.get_manifest_request_code.return_value = "code"
    manifest = MagicMock()
    self.get_manifest.return_value = manifest

    result = CDNClient.get_manifest_for_workshop_item(self, 12345)

    assert result is manifest
    assert manifest.name == "Hello"
    assert steam.send_um_and_wait.call_count == 3


def test_manifest_raises_after_all_timeouts(patched_method) -> None:
    from steam.client.cdn import CDNClient
    from steam.exceptions import SteamError

    steam = MagicMock()
    steam.connected = True
    steam.logged_on = True
    steam.send_um_and_wait.return_value = None

    self = MagicMock()
    self.steam = steam

    with pytest.raises(SteamError) as ei:
        CDNClient.get_manifest_for_workshop_item(self, 12345)

    assert "timed out" in str(ei.value).lower()
    assert steam.send_um_and_wait.call_count == UM_RETRIES


def test_manifest_no_steampipe_raises(patched_method) -> None:
    from steam.client.cdn import CDNClient
    from steam.exceptions import SteamError

    steam = MagicMock()
    steam.connected = True
    steam.logged_on = True
    resp = _make_ok_resp()
    resp.body.publishedfiledetails[0].hcontent_file = 0
    steam.send_um_and_wait.return_value = resp

    self = MagicMock()
    self.steam = steam

    with pytest.raises(SteamError) as ei:
        CDNClient.get_manifest_for_workshop_item(self, 1)
    assert "SteamPipe" in str(ei.value)
