"""Argparse + entry point."""

from __future__ import annotations

import argparse
import contextlib
import sys
import time
from collections.abc import Sequence
from pathlib import Path
from typing import NoReturn

from swd.constants import APP_NAME, DEFAULT_OUTPUT, DEFAULT_RETRIES, VERSION
from swd.dll import enable_vt_on_windows
from swd.download import download_item
from swd.steam import init_session, resolve_ids
from swd.ui import Log, Progress
from swd.utils import fmt_duration, fmt_size


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="swd",
        description=APP_NAME,
        epilog="Example: %(prog)s 294100 3683834622 3685058533",
    )
    p.add_argument("appid", type=int)
    p.add_argument("workshopid", type=int, nargs="+", help="one or more Workshop IDs to download")
    p.add_argument("-o", "--output", default=DEFAULT_OUTPUT)
    p.add_argument("-v", "--verbose", action="store_true")
    p.add_argument(
        "--proxy",
        metavar="URL",
        help="Proxy URL. Scheme selects protocol: socks5:// or socks5h:// "
        "(SOCKS5, default if scheme omitted), socks4:// (SOCKS4), "
        "http:// or https:// (HTTP CONNECT). "
        "Example: http://user:pass@127.0.0.1:8080. "
        "If omitted, no proxy is used (direct connection).",
    )
    p.add_argument(
        "--retries",
        type=int,
        default=DEFAULT_RETRIES,
        help=f"per-file retry attempts on failure (default: {DEFAULT_RETRIES})",
    )
    color_group = p.add_mutually_exclusive_group()
    color_group.add_argument(
        "--color",
        dest="color",
        action="store_true",
        default=None,
        help="force colored output (auto-detected by default)",
    )
    color_group.add_argument(
        "--no-color", dest="color", action="store_false", help="disable ANSI color output"
    )
    p.add_argument(
        "--log-file",
        metavar="PATH",
        default=None,
        type=Path,
        help="tee all human output (ANSI stripped) to this file in addition to stderr",
    )
    p.add_argument("--version", action="version", version=f"%(prog)s {VERSION}")
    return p


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    return build_parser().parse_args(argv)


def cmd_swd(argv: Sequence[str] | None = None) -> int:
    """Top-level entry point. Returns process exit code."""
    enable_vt_on_windows()
    try:
        args = parse_args(argv)
    except SystemExit as e:
        # argparse raises SystemExit on --help / --version / bad args.
        # Translate to int return so callers (and our tests) don't have to
        # catch it.
        return e.code if isinstance(e.code, int) else 1

    log = Log.create(
        use_color=args.color if args.color is not None else True, log_file=args.log_file
    )

    try:
        proxy_url = args.proxy
        output = Path(args.output).resolve()
        log.info(f"=== {APP_NAME} v{VERSION} ===")
        log.dim(f"  AppID: {args.appid}, IDs: {', '.join(str(w) for w in args.workshopid)}")
        log.dim(f"  Proxy: {proxy_url or '(direct connection)'}")
        log.dim(f"  Output: {output}")
        log.dim(f"  Retries per file: {args.retries}")

        client, cdn = init_session(proxy_url, log)
        if client is None:
            log.err("Failed to initialize session")
            return 1

        all_item_ids = resolve_ids(client, args.appid, args.workshopid, log)
        total_items = len(all_item_ids)

        if total_items == 0:
            log.warn("No valid workshop items to download")
            with contextlib.suppress(Exception):
                client.logout()  # type: ignore[attr-defined]
            return 1

        log.info(f"\n{total_items} item{'s' if total_items > 1 else ''} to download")

        run_start = time.perf_counter()
        total_ok = total_fail = total_bytes = 0
        failed_items = 0

        with Progress(total_items=total_items, log=log, verbose=args.verbose) as prog:
            for idx, wid in enumerate(all_item_ids, 1):
                prog.start_item(idx, str(wid))
                stats = download_item(
                    cdn,
                    args.appid,
                    wid,
                    output,
                    prog,
                    log,
                    verbose=args.verbose,
                    retries=args.retries,
                )
                prog.end_item()
                if stats is None or stats.fully_failed:
                    failed_items += 1
                else:
                    total_ok += stats.ok
                    total_fail += stats.fail
                    total_bytes += stats.bytes_done

        duration = time.perf_counter() - run_start
        with contextlib.suppress(Exception):
            client.logout()  # type: ignore[attr-defined]

        # Final summary block.
        log.blank()
        rule = "-" * 50
        log.info(rule)
        log.info(
            f"  Items:  {total_items} total / "
            f"{total_items - failed_items} ok / {failed_items} failed"
        )
        log.info(
            f"  Files:  {total_ok + total_fail} total / "
            f"{total_ok} ok / {total_fail} failed  "
            f"({fmt_size(total_bytes)})"
        )
        log.info(f"  Duration: {fmt_duration(duration)}")
        if total_bytes > 0 and duration > 0:
            avg = (total_bytes / (1024 * 1024)) / duration
            log.info(f"  Average:  {avg:.1f} MB/s")
        else:
            log.info("  Average:  n/a")
        log.info(rule)

        if failed_items:
            return 1
        log.info("All items downloaded.")
        return 0
    finally:
        log.close()


def main(argv: Sequence[str] | None = None) -> NoReturn:
    """Script-style entry that propagates the exit code through ``sys.exit``."""
    sys.exit(cmd_swd(argv))


__all__ = ["build_parser", "parse_args", "cmd_swd", "main"]
