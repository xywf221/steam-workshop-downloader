"""Smoke tests for the ctypes wrapper around ``steamclient64.dll``.

We never load a real DLL. ``ctypes.CDLL`` and ``ctypes.CFUNCTYPE`` are
patched so the loader can resolve symbols and the decompress wrapper
can be exercised against a fake dispatcher. ``c_void_p`` is left
alone — replacing it breaks ``decompress`` itself.
"""

from __future__ import annotations

import ctypes
from pathlib import Path
from unittest.mock import MagicMock

import pytest

from swd.constants import (
    RVA_DECOMPRESS_ALL,
)
from swd.dll import loader
from swd.dll.buffer import CUtlBuffer


@pytest.fixture
def fake_dll(monkeypatch, tmp_path: Path):
    """Replace ctypes.CDLL/CFUNCTYPE with mocks that record RVA offsets."""
    dll_file = tmp_path / "steamclient64.dll"
    dll_file.write_bytes(b"\x00" * 8)

    captured: list[tuple[str, object]] = []

    class FakeCDLL:
        _handle = 0x10000000
        _name = str(dll_file)

        def __init__(self, name, *args, **kwargs):
            captured.append(("CDLL", name))

    def fake_cfuntype(*_sig):
        def factory(addr):
            captured.append(("CFUNCTYPE", addr))
            return MagicMock(return_value=1)

        return factory

    monkeypatch.setattr(loader.ctypes, "CDLL", FakeCDLL)
    monkeypatch.setattr(loader.ctypes, "CFUNCTYPE", fake_cfuntype)
    return captured


@pytest.fixture(autouse=True)
def reset_loader_state():
    """Each test gets fresh global state for the loader."""
    loader._DLL = None  # type: ignore[attr-defined]
    loader._DECOMPRESS_ALL = None  # type: ignore[attr-defined]
    loader._PUT_FUNC = None  # type: ignore[attr-defined]
    loader._GET_FUNC = None  # type: ignore[attr-defined]
    yield
    loader._DLL = None  # type: ignore[attr-defined]
    loader._DECOMPRESS_ALL = None  # type: ignore[attr-defined]
    loader._PUT_FUNC = None  # type: ignore[attr-defined]
    loader._GET_FUNC = None  # type: ignore[attr-defined]


def test_load_dll_resolves_expected_rvas(fake_dll) -> None:
    loader.load_dll()
    rvas = [c[1] for c in fake_dll if c[0] == "CFUNCTYPE"]
    assert 0x10000000 + RVA_DECOMPRESS_ALL in rvas


def test_load_dll_missing_file_raises(tmp_path: Path) -> None:
    with pytest.raises(RuntimeError, match="not found"):
        loader.load_dll(path=tmp_path / "nope.dll")


def test_load_dll_is_idempotent(fake_dll) -> None:
    loader.load_dll()
    calls_after_first = len(fake_dll)
    loader.load_dll()  # second call: must not re-resolve
    assert len(fake_dll) == calls_after_first


def test_load_dll_default_path_uses_repo_root() -> None:
    p = loader.dll_path()
    assert p.name == "steamclient64.dll"
    # Sibling of pyproject.toml in development.
    assert (p.parent / "pyproject.toml").exists()


def _setup_mock_dispatcher(monkeypatch, return_code: int, written: bytes = b""):
    """Populate module-level state so ``decompress`` runs without a real DLL."""

    def dispatcher(data, size, buf_ptr, max_size, out_fmt):
        buf = ctypes.cast(buf_ptr, ctypes.POINTER(CUtlBuffer))[0]
        buf.tellPut = len(written)
        if written:
            ctypes.memmove(buf.data, written, len(written))
        return return_code

    fake_dispatcher = MagicMock(side_effect=dispatcher)
    monkeypatch.setattr(loader, "_DECOMPRESS_ALL", fake_dispatcher)
    monkeypatch.setattr(loader, "_PUT_FUNC", ctypes.c_void_p(0xDEAD))
    monkeypatch.setattr(loader, "_GET_FUNC", ctypes.c_void_p(0xBEEF))
    monkeypatch.setattr(loader, "_DLL", MagicMock(_name="fake.dll"))
    return fake_dispatcher


def test_decompress_returns_bytes(monkeypatch) -> None:
    _setup_mock_dispatcher(monkeypatch, return_code=1, written=b"hello world")
    out = loader.decompress(b"\x00" * 4)
    assert out == b"hello world"


def test_decompress_dispatcher_returns_error_propagates(monkeypatch) -> None:
    _setup_mock_dispatcher(monkeypatch, return_code=2)
    with pytest.raises(RuntimeError, match="Decompression failed"):
        loader.decompress(b"abcd")


def test_decompress_invalid_size_raises(monkeypatch) -> None:
    """Dispatcher wrote a zero-length output — must be rejected."""
    _setup_mock_dispatcher(monkeypatch, return_code=1, written=b"")
    with pytest.raises(RuntimeError, match="invalid size"):
        loader.decompress(b"abcd")
