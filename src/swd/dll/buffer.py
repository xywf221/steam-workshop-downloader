"""``steamclient64.dll`` integration: CUtlBuffer struct + VT mode."""

from __future__ import annotations

import ctypes
import os


class CUtlBuffer(ctypes.Structure):
    """Mirror of Steam's CUtlBuffer (reversed from sub_138CD1DB0).

    The dispatcher expects an output buffer wrapped in this struct. Offsets:

    =============  ====  ============================================================
    Offset         Size  Field
    =============  ====  ============================================================
    0x00           8     data pointer
    0x08           4     cbAllocated (m_nMaxReservedBytes)
    0x0C           4     reserved (m_nReservedBytes)
    0x10           4     tellGet (m_nOffset)
    0x14           4     tellPut (m_nBytesWritten)  ← written to by decompressor
    0x18           4     flags (m_nAccessFlags)
    0x1C           4     error (m_nError)
    0x20           8     putFunc (m_pPutFunc)
    0x28           8     getFunc (m_pGetFunc)
    Total: 0x30 = 48 bytes
    =============  ====  ============================================================
    """

    _fields_ = [
        ("data", ctypes.c_void_p),
        ("cbAllocated", ctypes.c_int),
        ("reserved", ctypes.c_int),
        ("tellGet", ctypes.c_int),
        ("tellPut", ctypes.c_int),
        ("flags", ctypes.c_int),
        ("error", ctypes.c_int),
        ("pad", ctypes.c_byte * 4),
        ("putFunc", ctypes.c_void_p),
        ("getFunc", ctypes.c_void_p),
    ]


def enable_vt_on_windows() -> bool:
    """Best-effort: turn on ``ENABLE_VIRTUAL_TERMINAL_PROCESSING`` on the
    current process's stdout and stderr handles so Windows consoles render
    ANSI escape codes. Returns ``True`` on success, ``False`` otherwise."""
    if os.name != "nt":
        return False
    try:
        kernel32 = ctypes.windll.kernel32
        ok = False
        for handle_id in (-11, -12):  # STD_OUTPUT_HANDLE, STD_ERROR_HANDLE
            handle = kernel32.GetStdHandle(handle_id)
            mode = ctypes.c_uint32()
            if kernel32.GetConsoleMode(handle, ctypes.byref(mode)) and kernel32.SetConsoleMode(
                handle, mode.value | 0x0004
            ):
                ok = True
        return ok
    except Exception:
        return False
