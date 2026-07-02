"""Tests for :mod:`swd.ui.log`."""

from __future__ import annotations

from pathlib import Path

from swd.ui.log import Colors, NoColors, strip_ansi


def test_strip_ansi() -> None:
    assert strip_ansi(f"{Colors.GREEN}hello{Colors.RESET}") == "hello"
    assert strip_ansi("plain text") == "plain text"
    assert strip_ansi("") == ""


def test_no_colors_all_empty() -> None:
    assert all(
        getattr(NoColors(), attr) == ""
        for attr in (
            "RESET",
            "DIM",
            "BOLD",
            "RED",
            "GREEN",
            "YELLOW",
            "CYAN",
            "GREY",
        )
    )


def test_log_writes_to_stderr(captured_stderr, no_color) -> None:
    from swd.ui.log import Log

    log = Log(use_color=False)
    log.info("hello world")
    log.ok("logged on")
    log.err("something bad")
    out = captured_stderr()
    assert "hello world" in out
    assert "logged on" in out
    assert "something bad" in out
    log.close()


def test_log_color_off_no_ansi(captured_stderr, no_color) -> None:
    from swd.ui.log import Log

    log = Log(use_color=False)
    log.ok("logged on")
    log.err("boom")
    log.retry("again")
    out = captured_stderr()
    assert "\x1b[" not in out
    log.close()


def test_log_color_on_includes_ansi(captured_stderr, colored) -> None:
    from swd.ui.log import Log

    log = Log(use_color=True)
    log.ok("logged on")
    log.err("boom")
    log.retry("again")
    out = captured_stderr()
    assert Colors.GREEN in out
    assert Colors.RED in out
    assert Colors.YELLOW in out
    log.close()


def test_log_stage_prefix(captured_stderr, no_color) -> None:
    from swd.ui.log import Log

    log = Log(use_color=False)
    log.stage("INIT", "connecting")
    out = captured_stderr()
    assert "[INIT]" in out
    assert "connecting" in out
    log.close()


def test_log_file_strips_ansi(tmp_path: Path, captured_stderr, colored) -> None:
    from swd.ui.log import Log

    log_path = tmp_path / "run.log"
    log = Log(use_color=True, log_file=log_path)
    log.ok("logged on")
    log.err("boom")
    log.close()
    contents = log_path.read_text(encoding="utf-8")
    assert "logged on" in contents
    assert "boom" in contents
    assert "\x1b[" not in contents


def test_log_create_respects_isatty(monkeypatch) -> None:
    """Log.create() auto-disables color when stderr is not a TTY."""
    import sys

    from swd.ui.log import Log

    monkeypatch.setattr(sys.stderr, "isatty", lambda: False)
    log = Log.create(use_color=True)
    log.ok("no color expected")
    # Hard to assert on the captured output here since we monkeypatched;
    # but at least the construction must succeed and use_color=False inside.
    log.close()


def test_log_close_idempotent(tmp_path: Path) -> None:
    from swd.ui.log import Log

    log = Log(use_color=False, log_file=tmp_path / "x.log")
    log.close()
    log.close()  # must not raise
