"""Small pure helpers reused across the package."""

from __future__ import annotations


def fmt_size(n: int) -> str:
    """Render a byte count as ``B`` / ``KB`` / ``MB``."""
    if n >= 1024 * 1024:
        return f"{n / 1024 / 1024:.1f} MB"
    if n >= 1024:
        return f"{n / 1024:.1f} KB"
    return f"{n} B"


def fmt_duration(seconds: float) -> str:
    """Render seconds as ``H:MM:SS`` (always zero-padded to 2-digit minutes/seconds)."""
    s = int(max(0, seconds))
    h, rem = divmod(s, 3600)
    m, sec = divmod(rem, 60)
    return f"{h}:{m:02d}:{sec:02d}"


def compute_backoff(attempt: int, cap: int = 30) -> int:
    """Exponential-ish retry backoff in seconds: 1, 2, 4, 8, ... clamped at ``cap``."""
    return min(2 ** max(0, attempt - 1), cap)
