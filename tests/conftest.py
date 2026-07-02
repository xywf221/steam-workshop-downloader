"""Shared pytest fixtures and helpers.

The DLL tests mock ctypes so they can run on any OS. Tests that touch the
``steam`` package get ``sys.modules['steam']`` pre-populated with a
MagicMock, so importing our package doesn't pull in ``steam[client]`` on
the test runner.
"""

from __future__ import annotations

import sys
import types
from unittest.mock import MagicMock

import pytest


def _install_steam_stub() -> None:
    """Stub the ``steam`` package so we don't need it installed to import ``swd``."""
    if "steam" in sys.modules:
        return
    steam = types.ModuleType("steam")
    client = types.ModuleType("steam.client")
    client_cdn = types.ModuleType("steam.client.cdn")
    client_cdn.CDNClient = MagicMock()
    core = types.ModuleType("steam.core")
    core_crypto = types.ModuleType("steam.core.crypto")
    core_crypto.symmetric_decrypt = MagicMock()
    exceptions = types.ModuleType("steam.exceptions")
    exceptions.SteamError = type("SteamError", (Exception,), {})

    sys.modules["steam"] = steam
    sys.modules["steam.client"] = client
    sys.modules["steam.client.cdn"] = client_cdn
    sys.modules["steam.core"] = core
    sys.modules["steam.core.crypto"] = core_crypto
    sys.modules["steam.exceptions"] = exceptions

    try:
        import tqdm.auto  # noqa: F401
    except ImportError:
        tqdm_mod = types.ModuleType("tqdm")
        tqdm_auto = types.ModuleType("tqdm.auto")

        class _NoOpBar:
            def __init__(self, *_args, **_kwargs):
                pass

            def update(self, *_args):
                return None

            def set_postfix_str(self, *_args, **_kwargs):
                return None

            def close(self):
                return None

            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

        tqdm_auto.tqdm = _NoOpBar
        tqdm_auto.tqdm.write = lambda *_a, **_k: None
        sys.modules["tqdm"] = tqdm_mod
        sys.modules["tqdm.auto"] = tqdm_auto


_install_steam_stub()

# Add the tests dir to sys.path at conftest-load time so plain
# `import _fakes` works without requiring `tests/` to be a package
# (no __init__.py).
import os as _os  # noqa: E402
import sys as _sys  # noqa: E402

_TESTS_DIR = _os.path.dirname(_os.path.abspath(__file__))
if _TESTS_DIR not in _sys.path:
    _sys.path.insert(0, _TESTS_DIR)

from _fakes import FakeCDN, FakeDir, FakeFile, FakeManifest  # noqa: E402,F401


@pytest.fixture
def fake_cdn() -> FakeCDN:
    return FakeCDN(manifests={})


@pytest.fixture
def no_color(monkeypatch):
    """Pretend stderr is not a TTY so :class:`Log` skips ANSI."""
    monkeypatch.setattr(sys.stderr, "isatty", lambda: False)


@pytest.fixture
def colored(monkeypatch):
    """Pretend stderr is a TTY so :class:`Log` emits ANSI."""
    monkeypatch.setattr(sys.stderr, "isatty", lambda: True)


@pytest.fixture
def captured_stderr(capsys):
    """Yield a function that returns the stderr captured so far.

    Capsys already manages the FD; we just hand callers a closure so the
    test reads ``get()`` after emitting.
    """

    class _Cap:
        def __init__(self):
            self._buf = ""

        def get(self) -> str:
            self._buf += capsys.readouterr().err
            return self._buf

    cap = _Cap()
    return cap.get


@pytest.fixture(autouse=True)
def _isolate_env(monkeypatch):
    yield
