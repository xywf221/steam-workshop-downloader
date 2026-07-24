"""Console entry: ``swd-web``."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from pathlib import Path

from swd.constants import (
    APP_NAME,
    DEFAULT_WEB_HOST,
    DEFAULT_WEB_OUTPUT,
    DEFAULT_WEB_PORT,
    VERSION,
)
from swd.dll import enable_vt_on_windows
from swd.ui.log import Log


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="swd-web",
        description=f"{APP_NAME} — Workshop browse / search / download web UI",
    )
    p.add_argument(
        "--host",
        default=DEFAULT_WEB_HOST,
        help=f"bind host (default: {DEFAULT_WEB_HOST})",
    )
    p.add_argument(
        "--port",
        type=int,
        default=DEFAULT_WEB_PORT,
        help=f"bind port (default: {DEFAULT_WEB_PORT})",
    )
    p.add_argument(
        "-o",
        "--output",
        default=DEFAULT_WEB_OUTPUT,
        metavar="DIR",
        help=f"directory for web-triggered downloads (default: {DEFAULT_WEB_OUTPUT})",
    )
    p.add_argument(
        "--proxy",
        metavar="URL",
        default=None,
        help="optional proxy for Steam CM (same schemes as swd --proxy)",
    )
    p.add_argument(
        "--debug",
        action="store_true",
        help="enable Flask debug reloader (dev only)",
    )
    p.add_argument("--version", action="version", version=f"%(prog)s {VERSION}")
    return p


def main(argv: Sequence[str] | None = None) -> int:
    enable_vt_on_windows()
    try:
        args = build_parser().parse_args(argv)
    except SystemExit as e:
        return e.code if isinstance(e.code, int) else 1

    try:
        import flask  # noqa: F401
    except ImportError:
        print(
            "Flask is required for swd-web. Install with: pip install 'swd[web]'",
            file=sys.stderr,
        )
        return 1

    output = Path(args.output).resolve()
    try:
        output.mkdir(parents=True, exist_ok=True)
    except OSError as e:
        print(f"Cannot create output directory {output}: {e}", file=sys.stderr)
        return 1

    log = Log.create(use_color=True)
    log.stage("WEB", f"Starting {APP_NAME} web UI on http://{args.host}:{args.port}")
    log.dim(f"  Download dir: {output}")
    if args.proxy:
        log.info(f"Proxy: {args.proxy}")

    from swd.web.app import create_app

    app = create_app(proxy_url=args.proxy, log=log, output_dir=output)
    # SteamClient is gevent-based and lives on dedicated worker threads inside
    # SteamSession (browse + download are separate so one never blocks the other).
    try:
        app.extensions["steam"].warmup()
    except Exception as e:  # pragma: no cover - defensive
        log.warn(f"Steam warmup error (browse): {e}")
    dl_steam = app.extensions.get("steam_download")
    if dl_steam is not None:
        try:
            dl_steam.warmup()
        except Exception as e:  # pragma: no cover - defensive
            log.warn(f"Steam warmup error (download): {e}")
    app.run(host=args.host, port=args.port, debug=args.debug, threaded=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
