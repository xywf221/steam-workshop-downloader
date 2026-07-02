"""Tests for :mod:`swd.utils`."""

from __future__ import annotations

import pytest

from swd.utils import compute_backoff, fmt_duration, fmt_size


@pytest.mark.parametrize(
    "n,expected",
    [
        (0, "0 B"),
        (1, "1 B"),
        (1023, "1023 B"),
        (1024, "1.0 KB"),
        (1536, "1.5 KB"),
        (1024 * 1024, "1.0 MB"),
        (1024 * 1024 * 2 + 512 * 1024, "2.5 MB"),
    ],
)
def test_fmt_size(n: int, expected: str) -> None:
    assert fmt_size(n) == expected


@pytest.mark.parametrize(
    "seconds,expected",
    [
        (0, "0:00:00"),
        (5, "0:00:05"),
        (59, "0:00:59"),
        (60, "0:01:00"),
        (3661, "1:01:01"),
        (7325, "2:02:05"),
    ],
)
def test_fmt_duration(seconds: float, expected: str) -> None:
    assert fmt_duration(seconds) == expected


def test_fmt_duration_negative_clamps_to_zero() -> None:
    assert fmt_duration(-1) == "0:00:00"


@pytest.mark.parametrize(
    "attempt,expected",
    [
        (1, 1),
        (2, 2),
        (3, 4),
        (4, 8),
        (5, 16),
        (6, 30),  # cap
        (7, 30),
    ],
)
def test_compute_backoff(attempt: int, expected: int) -> None:
    assert compute_backoff(attempt) == expected
