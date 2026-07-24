"""Nested ``tqdm`` progress bars + per-item/per-file event hooks."""

from __future__ import annotations

import sys
import threading
from dataclasses import dataclass
from pathlib import Path

import tqdm.auto as tqdm

from swd.ui.log import Colors, Log
from swd.utils import fmt_duration, fmt_size


@dataclass
class ItemStats:
    """Per-item download statistics returned by :func:`swd.download.item.download_item`."""

    out_dir: Path | None
    ok: int = 0
    fail: int = 0
    skipped: int = 0
    bytes_done: int = 0
    duration: float = 0.0
    name: str = ""

    @property
    def fully_failed(self) -> bool:
        """``True`` when nothing was kept (no download, no skip) but something failed.

        Used by the CLI to count "this item contributed nothing useful".
        """
        return self.ok == 0 and self.skipped == 0 and self.fail > 0


class Progress:
    """Two nested ``tqdm`` bars: outer item bar + inner file bar.

    When stderr is not a TTY both bars are ``None`` and the class falls back
    to plain Log lines (so piped output stays readable).

    All public mutators take an internal lock so worker threads from a
    multi-file download can call ``file_ok`` / ``retry`` / ``file_fail``
    concurrently without interleaving tqdm/Log output.
    """

    def __init__(self, total_items: int, log: Log, *, verbose: bool = False) -> None:
        self.log = log
        self.verbose = verbose
        self.total_items = total_items
        self._is_tty = sys.stderr.isatty()
        self.items_bar: tqdm.tqdm | None = None
        self.files_bar: tqdm.tqdm | None = None
        self._lock = threading.Lock()

    def __enter__(self) -> Progress:
        if self._is_tty and self.total_items > 0:
            self.items_bar = tqdm.tqdm(
                total=self.total_items,
                position=0,
                leave=True,
                desc="items",
                unit="item",
                dynamic_ncols=True,
                bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{eta}]",
            )
        return self

    def __exit__(self, *exc) -> None:
        if self.items_bar:
            self.items_bar.close()
            self.items_bar = None
        if self.files_bar:
            self.files_bar.close()
            self.files_bar = None

    def start_item(self, index_1based: int, label: str) -> None:
        idx_str = f"{index_1based}/{self.total_items}"
        self.log.info(f"=== Workshop {label}  ({idx_str}) ===")
        if self.items_bar is not None:
            self.items_bar.set_postfix_str(f"#{index_1based} {label}", refresh=True)

    def start_files(self, n_files: int) -> None:
        if self._is_tty and n_files > 0:
            self.files_bar = tqdm.tqdm(
                total=n_files,
                position=1,
                leave=False,
                desc="files",
                unit="file",
                dynamic_ncols=True,
                bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt}",
            )

    def file_ok(self, filename: str, size_bytes: int) -> None:
        with self._lock:
            if self.files_bar is not None:
                self.files_bar.update(1)
                self.files_bar.set_postfix_str(fmt_size(size_bytes), refresh=False)
                if self.verbose:
                    tqdm.tqdm.write(f"    {filename}  {fmt_size(size_bytes)}")
            else:
                # Non-TTY fallback: no bar to update.
                self.log.dim(f"    {filename}  {fmt_size(size_bytes)}")

    def file_skip(self, filename: str, size_bytes: int) -> None:
        """Local file already matches server SHA-1 — no CDN fetch."""
        with self._lock:
            if self.files_bar is not None:
                self.files_bar.update(1)
                self.files_bar.set_postfix_str(f"skip {fmt_size(size_bytes)}", refresh=False)
                if self.verbose:
                    tqdm.tqdm.write(
                        f"    {Colors.GREY}skip{Colors.RESET} {filename}  {fmt_size(size_bytes)}"
                    )
            else:
                self.log.dim(f"    skip {filename}  {fmt_size(size_bytes)}")

    def retry(
        self, filename: str, attempt: int, retries: int, err: Exception, backoff_s: int
    ) -> None:
        msg = f"{attempt}/{retries} on {filename}: {err} (backoff {backoff_s}s)"
        with self._lock:
            self.log.retry(msg)

    def file_fail(self, filename: str, err: Exception) -> None:
        with self._lock:
            if self.files_bar is not None:
                self.files_bar.update(1)
            if self._is_tty:
                tqdm.tqdm.write(f"    {Colors.RED}FAIL{Colors.RESET} {filename}: {err}")
            else:
                self.log.err(f"{filename}: {err}")

    def end_files(self, stats: ItemStats) -> None:
        with self._lock:
            if self.files_bar is not None:
                self.files_bar.close()
                self.files_bar = None
            total = stats.ok + stats.fail + stats.skipped
            if stats.out_dir is not None:
                parts = [f"{stats.ok} ok"]
                if stats.skipped:
                    parts.append(f"{stats.skipped} skip")
                parts.append(f"{stats.fail} fail")
                self.log.info(
                    f"  {fmt_size(stats.bytes_done)}  "
                    f"{total} files ({' / '.join(parts)})  "
                    f"in {fmt_duration(stats.duration)}  ->  {stats.out_dir}"
                )

    def end_item(self) -> None:
        with self._lock:
            if self.items_bar is not None:
                self.items_bar.update(1)
