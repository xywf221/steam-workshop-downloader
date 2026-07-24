"""Tests for :mod:`swd.steam.proxy`."""

from __future__ import annotations

import pytest

from swd.steam.proxy import parse_proxy_url


@pytest.mark.parametrize(
    "url,scheme,proto,host,port,user,pw",
    [
        ("socks5://1.2.3.4:1080", "socks5", "SOCKS5", "1.2.3.4", 1080, None, None),
        ("socks5h://1.2.3.4:1080", "socks5h", "SOCKS5", "1.2.3.4", 1080, None, None),
        ("socks4://1.2.3.4:1080", "socks4", "SOCKS4", "1.2.3.4", 1080, None, None),
        ("http://1.2.3.4:8080", "http", "HTTP", "1.2.3.4", 8080, None, None),
        ("https://1.2.3.4:8080", "https", "HTTP", "1.2.3.4", 8080, None, None),
        (
            "http://alice:secret@proxy.example.com:8080",
            "http",
            "HTTP",
            "proxy.example.com",
            8080,
            "alice",
            "secret",
        ),
        ("http://onlyuser@proxy:8080", "http", "HTTP", "proxy", 8080, "onlyuser", None),
        ("5.6.7.8:9100", "socks5", "SOCKS5", "5.6.7.8", 9100, None, None),
        # Upper-case scheme is fine
        ("HTTP://proxy:8080", "http", "HTTP", "proxy", 8080, None, None),
    ],
)
def test_parse_proxy_url_valid(url, scheme, proto, host, port, user, pw) -> None:
    p = parse_proxy_url(url)
    assert p.scheme == scheme
    assert p.proto_attr == proto
    assert p.host == host
    assert p.port == port
    assert p.username == user
    assert p.password == pw
    assert p.rdns is True


@pytest.mark.parametrize(
    "url",
    [
        "gopher://x:y/z",  # unsupported scheme
        "ftp://1.2.3.4:21",  # unsupported scheme
        "socks5://no-port",  # missing port
        "socks5://1.2.3.4:abc",  # bad port
    ],
)
def test_parse_proxy_url_invalid(url) -> None:
    with pytest.raises(ValueError):
        parse_proxy_url(url)


def test_setup_proxy_uses_pysocks(monkeypatch) -> None:
    """Verify setup_proxy calls into pysocks.set_default_proxy correctly."""
    captured = {}

    class FakeSocks:
        SOCKS5 = 5
        SOCKS4 = 4
        HTTP = 1

        @classmethod
        def set_default_proxy(cls, proto, host, port, rdns=None, username=None, password=None):
            captured["proto"] = proto
            captured["host"] = host
            captured["port"] = port
            captured["rdns"] = rdns
            captured["username"] = username
            captured["password"] = password

        class socksocket:
            pass

    import builtins

    orig_import = builtins.__import__

    def fake_import(name, *a, **k):
        if name == "socks":
            return FakeSocks
        if name == "socket":
            mod = type(__import__("types"))("socket")
            mod.socket = FakeSocks.socksocket
            mod.getaddrinfo = lambda *a, **k: [(2, 1, 6, "", ("127.0.0.1", 80))]
            return mod
        return orig_import(name, *a, **k)

    monkeypatch.setattr(builtins, "__import__", fake_import)
    from swd.steam import proxy as proxy_mod

    # Avoid permanently patching real TCPConnection with FakeSocks in this test.
    monkeypatch.setattr(proxy_mod, "patch_steam_cm_tcp", lambda: True)

    parsed = proxy_mod.setup_proxy("http://alice:secret@proxy:8080")

    assert captured["proto"] == FakeSocks.HTTP
    assert captured["host"] == "proxy"
    assert captured["port"] == 8080
    assert captured["rdns"] is True
    assert captured["username"] == "alice"
    assert captured["password"] == "secret"
    assert parsed.username == "alice"


def test_patch_steam_cm_tcp_replaces_connect(monkeypatch) -> None:
    """CM TCP connect must go through socks.socksocket, not raw gevent sockets."""
    pytest.importorskip("steam")
    pytest.importorskip("gevent")
    pytest.importorskip("socks")

    from steam.core.connection import TCPConnection

    from swd.steam.proxy import patch_steam_cm_tcp

    orig_new = TCPConnection._new_socket
    orig_conn = TCPConnection._connect
    had_flag = getattr(TCPConnection, "_swd_proxy_patched", False)
    # Force re-apply for this test.
    if had_flag:
        TCPConnection._swd_proxy_patched = False  # type: ignore[attr-defined]

    connected_to: list[tuple] = []

    class FakeSock:
        def __init__(self, *a, **k):
            self._timeout = None
            self._blocking = True

        def settimeout(self, t):
            self._timeout = t

        def setblocking(self, b):
            self._blocking = b

        def connect(self, addr):
            connected_to.append(addr)

        def detach(self):
            return 999999  # bogus fd — we will not wrap for real

        def close(self):
            pass

    import socks as real_socks

    monkeypatch.setattr(real_socks, "socksocket", FakeSock)

    # Avoid creating a real gevent socket from a fake fd.
    import gevent.socket as gsocket

    class FakeGreen:
        def __init__(self, *a, **k):
            pass

    monkeypatch.setattr(gsocket, "socket", FakeGreen)

    try:
        assert patch_steam_cm_tcp() is True
        assert TCPConnection._swd_proxy_patched is True  # type: ignore[attr-defined]
        conn = TCPConnection()
        conn._new_socket()
        assert conn.socket is None
        conn._connect(("203.0.113.1", 27017))
        assert connected_to == [("203.0.113.1", 27017)]
        assert isinstance(conn.socket, FakeGreen)
    finally:
        TCPConnection._new_socket = orig_new
        TCPConnection._connect = orig_conn
        TCPConnection._swd_proxy_patched = had_flag  # type: ignore[attr-defined]
