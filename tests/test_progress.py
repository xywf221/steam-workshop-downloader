"""Tests for :mod:`swd.ui.progress`."""

from __future__ import annotations

import pytest

from swd.ui import ItemStats, Log, Progress


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def test_fully_failed_property() -> None:
    assert not ItemStats(out_dir=None, ok=0, fail=0).fully_failed
    assert not ItemStats(out_dir=None, ok=2, fail=1).fully_failed
    assert ItemStats(out_dir=None, ok=0, fail=1).fully_failed
    assert ItemStats(out_dir=None, ok=0, fail=5).fully_failed


def test_progress_no_tty_no_bars(log: Log, captured_stderr) -> None:
    """In a non-TTY both bars must be None so output stays plain."""
    prog = Progress(total_items=3, log=log, verbose=False)
    assert prog.items_bar is None
    assert prog.files_bar is None
    with prog:
        prog.start_item(1, "111")
        prog.start_files(5)
        prog.file_ok("a.txt", 1024)
        prog.retry("b.txt", 1, 5, RuntimeError("boom"), 1)
        prog.file_fail("c.txt", RuntimeError("nope"))
        prog.end_files(ItemStats(out_dir=None, ok=2, fail=1, bytes_done=1024, duration=0.5))
        prog.end_item()


def test_progress_tty_creates_bars(log: Log, colored) -> None:
    prog = Progress(total_items=3, log=log, verbose=False)
    with prog:
        assert prog.items_bar is not None
        assert prog.items_bar.total == 3
        prog.start_item(1, "111")
        prog.start_files(2)
        prog.file_ok("a.txt", 1024)
        assert prog.files_bar is not None
        prog.end_files(ItemStats(out_dir=None, ok=1, fail=0, duration=0.1))
        prog.end_item()
        # After the second item, the items_bar should have advanced.
        prog.start_item(2, "222")
        prog.start_files(0)  # no file bar for empty manifests
        prog.end_files(ItemStats(out_dir=None, ok=0, fail=0, duration=0.0))
        prog.end_item()


def test_progress_total_items_zero_does_not_open_bar(log: Log, colored) -> None:
    prog = Progress(total_items=0, log=log)
    with prog:
        assert prog.items_bar is None


def test_progress_retry_message_includes_backoff(log: Log, captured_stderr) -> None:
    prog = Progress(total_items=1, log=log)
    with prog:
        prog.retry("a.bin", 2, 5, RuntimeError("timeout"), 4)
    out = captured_stderr()
    assert "2/5 on a.bin" in out
    assert "timeout" in out
    assert "backoff 4s" in out


def test_progress_end_files_logs_size_and_path(log: Log, captured_stderr, tmp_path) -> None:
    prog = Progress(total_items=1, log=log)
    with prog:
        prog.end_files(
            ItemStats(
                out_dir=tmp_path / "x",
                ok=3,
                fail=1,
                bytes_done=2048,
                duration=1.5,
                name="Mod",
            )
        )
    out = captured_stderr()
    assert "4 files" in out
    assert "3 ok" in out
    assert "1 fail" in out
    assert "2.0 KB" in out
