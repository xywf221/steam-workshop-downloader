"""Load ``steamclient64.dll`` and call its decompression dispatcher."""

from __future__ import annotations

import ctypes
import os
from pathlib import Path
from typing import Any

from swd.constants import MAX_CHUNK_SIZE, RVA_DECOMPRESS_ALL, RVA_GET_FUNC, RVA_PUT_FUNC
from swd.dll.buffer import CUtlBuffer

# Module-level handles, populated by load_dll().
_DLL: ctypes.CDLL | None = None
_DECOMPRESS_ALL: Any = None
_PUT_FUNC: ctypes.c_void_p | None = None
_GET_FUNC: ctypes.c_void_p | None = None


def dll_path() -> Path:
    """Default location of ``steamclient64.dll`` (sibling of the package)."""
    # src/swd/dll/loader.py -> project root
    return Path(__file__).resolve().parents[3] / "steamclient64.dll"


def is_loaded() -> bool:
    return _DECOMPRESS_ALL is not None


def load_dll(path: Path | None = None) -> Path:
    """Load ``steamclient64.dll`` and resolve the decompression symbols.

    Idempotent — second and later calls are no-ops. Returns the resolved path.
    Raises ``RuntimeError`` if the DLL is missing or cannot be loaded.
    """
    global _DLL, _DECOMPRESS_ALL, _PUT_FUNC, _GET_FUNC
    if _DECOMPRESS_ALL is not None:
        assert _DLL is not None
        return Path(_DLL._name)

    target = path or dll_path()
    if not target.exists():
        raise RuntimeError(
            f"steamclient64.dll not found at {target}. "
            "Place it next to pyproject.toml (or pass path=...) — the file "
            "comes with a steamcmd install and is required for VSZa chunk "
            "decompression."
        )

    # Make sure Windows can find the DLL's siblings (tier0, vstdlib).
    os.add_dll_directory(str(target.parent))
    _DLL = ctypes.CDLL(str(target))

    # sub_138CEAA90 — multi-format chunk decompression dispatcher.
    #   int __fastcall(data, size, CUtlBuffer*, max_size, out_format)
    #   returns: 1=ok, 2=error, 25=buffer too small, 53=CRC mismatch.
    _DECOMPRESS_ALL = ctypes.CFUNCTYPE(
        ctypes.c_int,  # return
        ctypes.c_void_p,  # rcx: input data
        ctypes.c_int,  # rdx: input size
        ctypes.c_void_p,  # r8:  CUtlBuffer* (output)
        ctypes.c_int,  # r9:  max output size
        ctypes.c_void_p,  # stack: out_format (optional)
    )(_DLL._handle + RVA_DECOMPRESS_ALL)

    _PUT_FUNC = ctypes.c_void_p(_DLL._handle + RVA_PUT_FUNC)
    _GET_FUNC = ctypes.c_void_p(_DLL._handle + RVA_GET_FUNC)

    return target


def decompress(data: bytes) -> bytes:
    """Decompress a single chunk (auto-detected format) via the DLL dispatcher.

    Raises ``RuntimeError`` on dispatcher failure or invalid output size.
    """
    load_dll()
    assert _DECOMPRESS_ALL is not None and _PUT_FUNC is not None and _GET_FUNC is not None

    out_buf = ctypes.create_string_buffer(MAX_CHUNK_SIZE)

    buf = CUtlBuffer()
    buf.data = ctypes.cast(out_buf, ctypes.c_void_p)
    buf.cbAllocated = MAX_CHUNK_SIZE
    buf.reserved = 0
    buf.tellGet = 0
    buf.tellPut = 0
    buf.flags = 0
    buf.error = 0
    buf.putFunc = _PUT_FUNC
    buf.getFunc = _GET_FUNC

    result = _DECOMPRESS_ALL(data, len(data), ctypes.byref(buf), MAX_CHUNK_SIZE, None)
    if result != 1:
        raise RuntimeError(f"Decompression failed (format={data[:4].hex()}, returned={result})")

    size = buf.tellPut
    if size <= 0 or size > MAX_CHUNK_SIZE:
        raise RuntimeError(f"Decompression produced invalid size {size}")

    return out_buf.raw[:size]


__all__ = ["dll_path", "load_dll", "decompress", "is_loaded"]
