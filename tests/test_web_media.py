"""Tests for Steam media URL validation + optional image proxy."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest

from swd.steam.workshop import QueryResult, WorkshopItem
from swd.ui import Log
from swd.web.media import (
    display_preview_url,
    fetch_media,
    is_allowed_media_host,
    validate_media_url,
)

flask = pytest.importorskip("flask")


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def test_allowed_hosts() -> None:
    assert is_allowed_media_host("steamuserimages-a.akamaihd.net")
    assert is_allowed_media_host("cdn.akamai.steamstatic.com")
    assert is_allowed_media_host("shared.cloudflare.steamstatic.com")
    assert is_allowed_media_host("steamcommunity.com")
    assert not is_allowed_media_host("evil.example.com")
    assert not is_allowed_media_host("127.0.0.1")
    assert not is_allowed_media_host("192.168.1.1")
    assert not is_allowed_media_host("localhost")


def test_validate_media_url_ok() -> None:
    u = validate_media_url("https://steamuserimages-a.akamaihd.net/ugc/123/abc.jpg")
    assert u.startswith("https://steamuserimages-a.akamaihd.net/")


def test_validate_media_url_rejects_bad() -> None:
    with pytest.raises(ValueError):
        validate_media_url("https://evil.example/x.png")
    with pytest.raises(ValueError):
        validate_media_url("file:///etc/passwd")
    with pytest.raises(ValueError):
        validate_media_url("https://127.0.0.1/secret")


def test_display_preview_url_direct_without_proxy() -> None:
    src = "https://steamuserimages-a.akamaihd.net/ugc/1.jpg"
    assert display_preview_url(src, proxy_url=None) == src


def test_display_preview_url_rewrites_with_proxy() -> None:
    src = "https://steamuserimages-a.akamaihd.net/ugc/1.jpg"
    out = display_preview_url(src, proxy_url="socks5://127.0.0.1:1080")
    assert out.startswith("/media/image?u=")
    assert "steamuserimages" in out


def test_fetch_media_success() -> None:
    class _Resp:
        def __init__(self) -> None:
            self.headers = MagicMock()
            self.headers.get_content_type.return_value = "image/jpeg"
            self._data = [b"imagedata", b""]

        def read(self, _n: int = -1) -> bytes:
            return self._data.pop(0)

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

    opener = MagicMock()
    opener.open.return_value = _Resp()
    with patch("swd.web.media.build_opener", return_value=opener):
        body, ctype = fetch_media(
            "https://steamuserimages-a.akamaihd.net/ugc/x.jpg",
            proxy_url=None,
        )
    assert body == b"imagedata"
    assert ctype == "image/jpeg"


def test_media_route_proxies_bytes(log: Log) -> None:
    from swd.web.app import create_app

    steam = MagicMock()
    steam.logged_on = False
    steam.has_client = False
    steam.call.side_effect = AssertionError("no steam")

    app = create_app(proxy_url="socks5://127.0.0.1:1080", log=log, steam=steam)
    app.config["TESTING"] = True

    with (
        patch("swd.web.app.setup_proxy", create=True),
        patch("swd.steam.proxy.setup_proxy", return_value=None),
        patch(
            "swd.web.app.fetch_media",
            return_value=(b"\xff\xd8fakejpeg", "image/jpeg"),
        ) as fm,
        app.test_client() as c,
    ):
        url = "https://steamuserimages-a.akamaihd.net/ugc/abc.jpg"
        rv = c.get("/media/image", query_string={"u": url})
        assert rv.status_code == 200
        assert rv.data == b"\xff\xd8fakejpeg"
        assert rv.mimetype == "image/jpeg"
        fm.assert_called_once()
        assert fm.call_args.args[0] == url
        assert fm.call_args.kwargs.get("proxy_url") == "socks5://127.0.0.1:1080"


def test_media_route_rejects_evil_host(log: Log) -> None:
    from swd.web.app import create_app

    steam = MagicMock()
    steam.logged_on = False
    steam.has_client = False
    app = create_app(log=log, steam=steam)
    app.config["TESTING"] = True
    with app.test_client() as c:
        rv = c.get("/media/image", query_string={"u": "https://evil.example/a.png"})
        assert rv.status_code == 400


def test_html_uses_proxy_src_when_proxy_configured(log: Log) -> None:
    from swd.steam.workshop import query_files
    from swd.web.app import create_app

    item = WorkshopItem(
        id=7,
        title="Combat AI",
        short_description="x",
        preview_url="https://steamuserimages-a.akamaihd.net/ugc/p.jpg",
        file_size=1,
        subscriptions=0,
        favorited=0,
        views=0,
        time_created=0,
        time_updated=0,
        file_type=0,
        creator=1,
        consumer_appid=294100,
        tags=[],
        vote_score=None,
        banned=False,
    )
    result = QueryResult(total=1, items=[item], page=1, numperpage=20)
    steam = MagicMock()
    steam.logged_on = True
    steam.has_client = True

    def _call(fn, *args, **kwargs):
        assert fn is query_files
        return result

    steam.call.side_effect = _call

    with (
        patch("swd.steam.proxy.setup_proxy", return_value=None),
        patch("swd.web.app.setup_proxy", create=True),
    ):
        app = create_app(proxy_url="socks5://127.0.0.1:1080", log=log, steam=steam)
        app.config["TESTING"] = True
        with app.test_client() as c:
            rv = c.get("/?appid=294100")
            assert rv.status_code == 200
            assert b"/media/image?u=" in rv.data
            assert b"Combat AI" in rv.data
            # Direct CDN URL must not be the img src when proxy is on.
            assert b'src="https://steamuserimages-a.akamaihd.net' not in rv.data
