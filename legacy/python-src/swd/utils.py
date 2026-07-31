"""Small pure helpers reused across the package."""

from __future__ import annotations

from swd.constants import MAX_AUTO_JOBS


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


def suggest_jobs(n_files: int, *, max_jobs: int = MAX_AUTO_JOBS) -> int:
    """Pick a worker count for downloading ``n_files`` files of one item.

    Heuristic (I/O-bound CDN fetches, not CPU):

    * 0 / 1 file → 1 worker (no pool overhead)
    * a few files → one worker per file
    * many files → soft-cap at ``max_jobs`` (default :data:`MAX_AUTO_JOBS`)
      so we don't stampede the CDN or the local proxy

    ``max_jobs`` is clamped to at least 1. ``n_files`` ≤ 0 yields 1.
    """
    cap = max(1, int(max_jobs))
    n = max(0, int(n_files))
    if n <= 1:
        return 1
    return min(n, cap)
