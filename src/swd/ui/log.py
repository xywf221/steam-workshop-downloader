"""Structured human output: stage-prefixed, ANSI-coloured, file-teeable."""

from __future__ import annotations

import re
import sys
from pathlib import Path


class Colors:
    """ANSI escape sequences. Empty strings when colours are disabled."""

    RESET = "\033[0m"
    DIM = "\033[2m"
    BOLD = "\033[1m"
    RED = "\033[31m"
    GREEN = "\033[32m"
    YELLOW = "\033[33m"
    CYAN = "\033[36m"
    GREY = "\033[90m"


class NoColors:
    """Drop-in replacement for :class:`Colors` with all attributes empty."""

    RESET = ""
    DIM = ""
    BOLD = ""
    RED = ""
    GREEN = ""
    YELLOW = ""
    CYAN = ""
    GREY = ""


_ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")


def strip_ansi(s: str) -> str:
    """Strip all ANSI escape codes from ``s``."""
    return _ANSI_RE.sub("", s)


class Log:
    """Single sink for human output. Writes to stderr and optionally tees every
    line to a log file (ANSI stripped).

    Construct with :meth:`create` rather than calling ``__init__`` directly so
    the colour-on-by-default + TTY-detect behaviour is centralised.
    """

    def __init__(self, *, use_color: bool, log_file: Path | None = None) -> None:
        self._c = Colors if use_color else NoColors
        self._r = self._c.RESET
        self._fp = (
            open(log_file, "a", encoding="utf-8") if log_file else None  # noqa: SIM115
        )

    @classmethod
    def create(cls, *, use_color: bool = True, log_file: Path | None = None) -> Log:
        """Construct a Log with sensible defaults: colour on only if TTY."""
        effective_color = use_color and sys.stderr.isatty()
        return cls(use_color=effective_color, log_file=log_file)

    def _emit(self, line: str) -> None:
        sys.stderr.write(line + "\n")
        sys.stderr.flush()
        if self._fp is not None:
            self._fp.write(strip_ansi(line) + "\n")
            self._fp.flush()

    def stage(self, stage: str, msg: str) -> None:
        self._emit(f"{self._c.CYAN}[{stage}]{self._r} {msg}")

    def info(self, msg: str) -> None:
        self._emit(msg)

    def ok(self, msg: str) -> None:
        self._emit(f"{self._c.GREEN}OK{self._r}  {msg}")

    def warn(self, msg: str) -> None:
        self._emit(f"{self._c.YELLOW}!!{self._r}  {msg}")

    def err(self, msg: str) -> None:
        self._emit(f"{self._c.RED}FAIL{self._r} {msg}")

    def dim(self, msg: str) -> None:
        self._emit(f"{self._c.DIM}{msg}{self._r}")

    def retry(self, msg: str) -> None:
        self._emit(f"{self._c.YELLOW}-> retry:{self._r} {msg}")

    def blank(self) -> None:
        self._emit("")

    def close(self) -> None:
        if self._fp is not None:
            self._fp.close()
            self._fp = None
