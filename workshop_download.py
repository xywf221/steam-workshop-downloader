#!/usr/bin/env python3
"""
Steam Workshop Offline Downloader v5
=====================================
Pure Python Steam Workshop downloader.
Uses ValvePython/steam for Steam protocol.
All chunk decompression delegated to steamclient64.dll via ctypes.

Usage: pip install steam[client] pysocks tqdm
       python workshop_download.py <AppID> <WorkshopID> [<WorkshopID>...] [options]
       python workshop_download.py 294100 3683834622 3685058533
       python workshop_download.py 294100 3047389309  (auto-expands collection)
       python workshop_download.py 294100 3683834622 --retries 10
"""

from __future__ import annotations

import argparse
import ctypes
import os
import re
import struct
import sys
import time
from binascii import crc32
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

import tqdm.auto as tqdm

# ctypes cache
_DLL = None
_DECOMPRESS_ALL = None  # sub_138CEAA90 (multi-format dispatcher)
_PUT_FUNC = None       # CUtlBuffer.putFunc  (RVA 0xEB570, no-op)
_GET_FUNC = None       # CUtlBuffer.getFunc  (RVA 0xD3F20)
_MAX_CHUNK_SIZE = 100 * 1024 * 1024  # 100 MB safety limit


class _CUtlBuffer(ctypes.Structure):
    """Reversed from sub_138CD1DB0 (CUtlBuffer init).

    Offset  Size  Field
    0x00    8     data pointer
    0x08    4     cbAllocated (m_nMaxReservedBytes)
    0x0C    4     reserved (m_nReservedBytes)
    0x10    4     tellGet (m_nOffset)
    0x14    4     tellPut (m_nBytesWritten)  ← used as write position
    0x18    4     flags (m_nAccessFlags)
    0x1C    4     error (m_nError)
    0x20    8     putFunc (m_pPutFunc)
    0x28    8     getFunc (m_pGetFunc)
    Total: 0x30 = 48 bytes
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


def _load_dll():
    """Load steamclient64.dll and resolve decompression functions."""
    global _DLL, _DECOMPRESS_ALL, _PUT_FUNC, _GET_FUNC
    if _DECOMPRESS_ALL is not None:
        return
    dll_path = Path(__file__).parent / "steamclient64.dll"
    if not dll_path.exists():
        raise RuntimeError(
            f"steamclient64.dll not found at {dll_path}. "
            "Steamclient64.dll (from steamcmd install) is required "
            "for VSZa chunk decompression."
        )
    os.add_dll_directory(str(dll_path.parent))
    _DLL = ctypes.CDLL(str(dll_path))

    # sub_138CEAA90: multi-format chunk decompression dispatcher
    #   signature: int __fastcall(data, size, CUtlBuffer*, max_size, out_format)
    #   returns: 1=ok, 2=error, 25=buffer too small, 53=CRC mismatch
    _DECOMPRESS_ALL = ctypes.CFUNCTYPE(
        ctypes.c_int,       # return
        ctypes.c_void_p,    # rcx: input data
        ctypes.c_int,       # rdx: input size
        ctypes.c_void_p,    # r8:  CUtlBuffer* (output)
        ctypes.c_int,       # r9:  max output size
        ctypes.c_void_p,    # stack: out_format (optional, can be None)
    )(_DLL._handle + 0xCEAA90)

    # CUtlBuffer function pointers (extracted from init function)
    _PUT_FUNC = ctypes.c_void_p(_DLL._handle + 0xEB570)
    _GET_FUNC = ctypes.c_void_p(_DLL._handle + 0xD3F20)


def _dll_decompress(data: bytes) -> bytes:
    """Decompress a chunk (any format) using steamclient64.dll dispatcher."""
    _load_dll()

    # Pre-allocate output buffer
    out_buf = ctypes.create_string_buffer(_MAX_CHUNK_SIZE)

    # Construct CUtlBuffer pointing at our output buffer
    buf = _CUtlBuffer()
    buf.data = ctypes.cast(out_buf, ctypes.c_void_p)
    buf.cbAllocated = _MAX_CHUNK_SIZE
    buf.reserved = 0
    buf.tellGet = 0
    buf.tellPut = 0
    buf.flags = 0
    buf.error = 0
    buf.putFunc = _PUT_FUNC
    buf.getFunc = _GET_FUNC

    # Call the multi-format dispatcher
    # It auto-detects format: VSZa, VZa, gzip, ZIP, raw LZMA
    result = _DECOMPRESS_ALL(data, len(data), ctypes.byref(buf),
                             _MAX_CHUNK_SIZE, None)

    if result != 1:
        raise RuntimeError(
            f"Decompression failed (format={data[:4].hex()}, "
            f"returned={result})"
        )

    decompressed_size = buf.tellPut
    if decompressed_size <= 0 or decompressed_size > _MAX_CHUNK_SIZE:
        raise RuntimeError(
            f"Decompression produced invalid size {decompressed_size}"
        )

    return out_buf.raw[:decompressed_size]


def setup_proxy(proxy_url: str):
    """Route all outbound sockets through a proxy.

    Protocol is selected by URL scheme:

    - ``socks5://host:port``  / ``socks5h://host:port``  → SOCKS5
    - ``socks4://host:port``                              → SOCKS4
    - ``http://host:port``  / ``https://host:port``       → HTTP CONNECT
    - bare ``host:port``                                  → SOCKS5 (default)

    For HTTP/HTTPS proxies, ``user:password@`` in the URL is honoured
    (passed as the ``Proxy-Authorization`` header by pysocks).
    """
    import socks, socket

    raw = proxy_url.strip()
    m = re.match(r"^([a-zA-Z][a-zA-Z0-9+.\-]*)://(.*)$", raw)
    if m:
        scheme = m.group(1).lower()
        rest = m.group(2)
    else:
        scheme = "socks5"
        rest = raw

    if scheme in ("socks5", "socks5h"):
        proto = socks.SOCKS5
    elif scheme == "socks4":
        proto = socks.SOCKS4
    elif scheme in ("http", "https"):
        proto = socks.HTTP
    else:
        raise ValueError(
            f"Unsupported proxy scheme {scheme!r} in {proxy_url!r} "
            "(expected socks5://, socks4://, or http(s)://)"
        )

    # Split off optional userinfo (only meaningful for HTTP CONNECT).
    user = None
    password = None
    if "@" in rest:
        userinfo, rest = rest.rsplit("@", 1)
        if ":" in userinfo:
            user, password = userinfo.split(":", 1)
        else:
            user = userinfo

    if ":" not in rest:
        raise ValueError(
            f"Invalid proxy URL {proxy_url!r}: expected host:port "
            "(e.g. socks5://127.0.0.1:1080 or http://user:pass@proxy:8080)"
        )
    host, port = rest.rsplit(":", 1)
    try:
        port_num = int(port)
    except ValueError as e:
        raise ValueError(f"Invalid proxy port in {proxy_url!r}: {port!r}") from e

    socks.set_default_proxy(proto, host, port_num, rdns=True,
                            username=user, password=password)
    socket.socket = socks.socksocket


def patch_vsza():
    """Monkey-patch CDNClient.get_chunk to use DLL decompression."""
    from steam.client.cdn import CDNClient
    from steam.core.crypto import symmetric_decrypt
    from steam.exceptions import SteamError

    def vsza_get_chunk(self, app_id, depot_id, chunk_id):
        key = (depot_id, chunk_id)
        if key not in self._chunk_cache:
            resp = self.cdn_cmd("depot", "%s/chunk/%s" % (depot_id, chunk_id))
            encrypted = symmetric_decrypt(
                resp.content, self.get_depot_key(app_id, depot_id)
            )
            try:
                data = _dll_decompress(encrypted)
            except Exception as e:
                raise SteamError(f"DLL decompress: {e}") from e
            self._chunk_cache[key] = data
        return self._chunk_cache[key]

    CDNClient.get_chunk = vsza_get_chunk


def _init_session(proxy_url: Optional[str], log: Log):
    """Initialize Steam session (login + CDN).

    If ``proxy_url`` is None, no proxy is configured and connections go out
    directly. Pass an explicit URL (e.g. ``socks5://127.0.0.1:1080``) to
    route through a SOCKS5 proxy.
    """
    if proxy_url is not None:
        setup_proxy(proxy_url)
    patch_vsza()

    from steam.client import SteamClient
    from steam.client.cdn import CDNClient

    log.stage("INIT", "Connecting to Steam...")
    client = SteamClient()
    if client.anonymous_login() != 1:
        log.err("Login failed")
        return None, None
    log.ok(f"Logged on ({client.steam_id})")

    log.stage("INIT", "Getting content servers...")
    cdn = CDNClient(client)
    log.ok(f"Server: {cdn.get_content_server()}")
    return client, cdn


def _fmt_size(n: int) -> str:
    if n >= 1024 * 1024:
        return f"{n / 1024 / 1024:.1f} MB"
    if n >= 1024:
        return f"{n / 1024:.1f} KB"
    return f"{n} B"


def _fmt_duration(seconds: float) -> str:
    s = int(max(0, seconds))
    h, rem = divmod(s, 3600)
    m, sec = divmod(rem, 60)
    return f"{h}:{m:02d}:{sec:02d}"


# --- Logging & progress ------------------------------------------------------

class _Colors:
    """ANSI escape sequences. Empty string when colors are disabled."""
    RESET = "\033[0m"
    DIM = "\033[2m"
    BOLD = "\033[1m"
    RED = "\033[31m"
    GREEN = "\033[32m"
    YELLOW = "\033[33m"
    CYAN = "\033[36m"
    GREY = "\033[90m"


def _enable_vt_on_windows() -> None:
    """Best-effort: turn on ENABLE_VIRTUAL_TERMINAL_PROCESSING so cmd/PowerShell
    render ANSI escape codes. Silently no-ops on failure or non-Windows."""
    if os.name != "nt":
        return
    try:
        kernel32 = ctypes.windll.kernel32
        for handle_id in (-11, -12):  # STD_OUTPUT_HANDLE, STD_ERROR_HANDLE
            handle = kernel32.GetStdHandle(handle_id)
            mode = ctypes.c_uint32()
            if kernel32.GetConsoleMode(handle, ctypes.byref(mode)):
                kernel32.SetConsoleMode(
                    handle, mode.value | 0x0004  # ENABLE_VIRTUAL_TERMINAL_PROCESSING
                )
    except Exception:
        pass


_enable_vt_on_windows()


_ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")


def _strip_ansi(s: str) -> str:
    return _ANSI_RE.sub("", s)


@dataclass
class ItemStats:
    """Per-item download statistics returned by ``download_item``."""
    out_dir: Optional[Path]
    ok: int = 0
    fail: int = 0
    bytes_done: int = 0
    duration: float = 0.0
    name: str = ""

    @property
    def fully_failed(self) -> bool:
        """True when zero files succeeded but at least one failed.

        Used by ``main`` to count "item contributed nothing useful."
        """
        return self.ok == 0 and self.fail > 0


class Log:
    """Structured human output. Writes to stderr (so tqdm bars coexist) and
    optionally tees every line to a log file (ANSI stripped)."""

    def __init__(self, *, use_color: bool = True, log_file: Optional[Path] = None):
        if use_color and sys.stderr.isatty():
            self._c = _Colors
            self._r = _Colors.RESET
        else:
            self._c = type("NoColor", (), {
                k: "" for k in ("RESET", "DIM", "BOLD", "RED", "GREEN",
                                "YELLOW", "CYAN", "GREY")
            })()
            self._r = ""
        self._fp = open(log_file, "a", encoding="utf-8") if log_file else None

    def _emit(self, line: str) -> None:
        sys.stderr.write(line + "\n")
        sys.stderr.flush()
        if self._fp is not None:
            self._fp.write(_strip_ansi(line) + "\n")
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


class Progress:
    """Two nested ``tqdm`` bars: outer item bar + inner file bar. When stderr
    is not a TTY, both bars are ``None`` and the class falls back to plain
    Log output (so piping to a file keeps working)."""

    def __init__(self, total_items: int, log: Log, *, verbose: bool = False):
        self.log = log
        self.verbose = verbose
        self.total_items = total_items
        self._is_tty = sys.stderr.isatty()
        self.items_bar: Optional[tqdm.tqdm] = None
        self.files_bar: Optional[tqdm.tqdm] = None
        self._active_item_idx = 0

    def __enter__(self) -> "Progress":
        if self._is_tty and self.total_items > 0:
            self.items_bar = tqdm.tqdm(
                total=self.total_items, position=0, leave=True,
                desc="items", unit="item", dynamic_ncols=True,
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
        self._active_item_idx = index_1based
        idx_str = f"{index_1based}/{self.total_items}"
        self.log.info(f"=== Workshop {label}  ({idx_str}) ===")
        if self.items_bar is not None:
            self.items_bar.set_postfix_str(f"#{index_1based} {label}", refresh=True)

    def start_files(self, n_files: int) -> None:
        if self._is_tty and n_files > 0:
            self.files_bar = tqdm.tqdm(
                total=n_files, position=1, leave=False,
                desc="files", unit="file", dynamic_ncols=True,
                bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt}",
            )

    def file_ok(self, filename: str, size_bytes: int) -> None:
        if self.files_bar is not None:
            self.files_bar.update(1)
            self.files_bar.set_postfix_str(_fmt_size(size_bytes), refresh=False)
            if self.verbose:
                tqdm.tqdm.write(f"    {filename}  {_fmt_size(size_bytes)}")
        else:
            # Non-TTY fallback: no bar to update, so emit a dim line.
            self.log.dim(f"    {filename}  {_fmt_size(size_bytes)}")

    def retry(self, filename: str, attempt: int, retries: int,
              err: Exception, backoff_s: int) -> None:
        msg = (f"{attempt}/{retries} on {filename}: {err} "
               f"(backoff {backoff_s}s)")
        self.log.retry(msg)

    def file_fail(self, filename: str, err: Exception) -> None:
        if self.files_bar is not None:
            self.files_bar.update(1)
        if self._is_tty:
            tqdm.tqdm.write(f"    {self._c_for_bar().RED}FAIL{self._c_for_bar().RESET} "
                            f"{filename}: {err}")
        else:
            self.log.err(f"{filename}: {err}")

    def end_files(self, stats: "ItemStats") -> None:
        if self.files_bar is not None:
            self.files_bar.close()
            self.files_bar = None
        total = stats.ok + stats.fail
        if stats.out_dir is not None:
            self.log.info(
                f"  {_fmt_size(stats.bytes_done)}  "
                f"{total} files ({stats.ok} ok / {stats.fail} fail)  "
                f"in {_fmt_duration(stats.duration)}  ->  {stats.out_dir}"
            )

    def end_item(self) -> None:
        if self.items_bar is not None:
            self.items_bar.update(1)

    def _c_for_bar(self) -> _Colors:
        """tqdm.write runs before our Log coloring layer, so return a fresh
        Colors object whose attributes are empty when colors are off."""
        return _Colors


def download_item(cdn, app_id: int, workshop_id: int, output_dir: Path,
                  progress: Progress, log: Log,
                  verbose: bool = False, retries: int = 5) -> Optional[ItemStats]:
    """Download a single workshop item using an existing CDN session.

    Each individual file is retried up to ``retries`` times on failure
    (network errors, decompression errors, etc.) before being marked as
    failed. A small exponential backoff is applied between attempts.

    Returns an ``ItemStats`` summarising the run. Returns ``None`` only when
    no output directory could be created at all (which currently cannot
    happen given ``mkdir(parents=True, exist_ok=True)``).
    """
    t0 = time.perf_counter()
    log.stage("MANIFEST", f"Fetching manifest for {workshop_id}...")
    manifest = cdn.get_manifest_for_workshop_item(workshop_id)
    files = list(manifest.iter_files())
    item_name = manifest.name or str(workshop_id)
    log.ok(f"'{item_name}' - {len(files)} files")

    out_dir = output_dir / str(workshop_id)
    out_dir.mkdir(parents=True, exist_ok=True)
    progress.start_files(len(files))

    ok = fail = bytes_done = 0
    for f in files:
        if not f.is_file:
            (out_dir / f.filename).mkdir(parents=True, exist_ok=True)
            continue
        path = out_dir / f.filename
        path.parent.mkdir(parents=True, exist_ok=True)

        # Remove any partial file from a previous failed attempt so the
        # final size check (below) reflects only completed downloads.
        if path.exists():
            try:
                path.unlink()
            except OSError:
                pass

        success = False
        last_err = None
        for attempt in range(1, retries + 1):
            try:
                data = f.read()
                with open(path, "wb") as fp:
                    fp.write(data)
                ok += 1
                bytes_done += len(data)
                success = True
                progress.file_ok(f.filename, len(data))
                break
            except Exception as e:
                last_err = e
                if attempt < retries:
                    # exponential-ish backoff: 1s, 2s, 4s, ...
                    wait = min(2 ** (attempt - 1), 30)
                    progress.retry(f.filename, attempt, retries, e, wait)
                    time.sleep(wait)
                    # Re-fetch the manifest entry so any internal chunk cache
                    # state from the failed attempt doesn't poison the next try.
                    try:
                        f = next(iter(manifest.iter_files()))
                    except Exception:
                        pass

        if not success:
            fail += 1
            progress.file_fail(f.filename, last_err)
            # Best-effort cleanup of any zero-byte stub we may have written.
            if path.exists():
                try:
                    path.unlink()
                except OSError:
                    pass

    stats = ItemStats(
        out_dir=out_dir,
        ok=ok, fail=fail,
        bytes_done=bytes_done,
        duration=time.perf_counter() - t0,
        name=item_name,
    )
    progress.end_files(stats)
    return stats


def _resolve_ids(client, app_id: int, ids, log: Log) -> list:
    """Expand collections (file_type==2) into individual workshop IDs."""
    from steam.client import EResult

    all_ids = list(dict.fromkeys(ids))
    resolved = []

    for start in range(0, len(all_ids), 100):
        batch = all_ids[start:start + 100]
        resp = client.send_um_and_wait('PublishedFile.GetDetails#1', {
            'publishedfileids': batch,
            'includetags': False,
            'includeadditionalpreviews': False,
            'includechildren': True,
            'includekvtags': False,
            'includevotes': False,
            'short_description': True,
            'includeforsaledata': False,
            'includemetadata': False,
            'language': 0
        }, timeout=10)

        for wf in resp.body.publishedfiledetails:
            if wf.result != EResult.OK:
                log.warn(f"failed to get details for {wf.publishedfileid}, skipping")
                continue

            wid = wf.publishedfileid
            title = wf.title or str(wid)
            if wf.file_type == 2:  # Collection
                child_ids = [c.publishedfileid for c in wf.children]
                log.info(f"  '{title}' is a collection, {len(child_ids)} items")
                resolved.extend(_resolve_ids(client, app_id, child_ids, log))
            else:
                resolved.append(wid)

    return resolved


def parse_args():
    p = argparse.ArgumentParser(
        description="Steam Workshop Downloader v5",
        epilog="Example: %(prog)s 294100 3683834622 3685058533"
    )
    p.add_argument("appid", type=int)
    p.add_argument("workshopid", type=int, nargs="+",
                   help="one or more Workshop IDs to download")
    p.add_argument("-o", "--output", default=".")
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
    p.add_argument("--retries", type=int, default=5,
                   help="number of retry attempts for a failed file download (default: 5)")
    color_group = p.add_mutually_exclusive_group()
    color_group.add_argument("--color", dest="color", action="store_true",
                             default=None,
                             help="force colored output (auto-detected by default)")
    color_group.add_argument("--no-color", dest="color", action="store_false",
                             help="disable ANSI color output")
    p.add_argument("--log-file", metavar="PATH", default=None, type=Path,
                   help="tee all human output (ANSI stripped) to this file "
                        "in addition to stderr")
    return p.parse_args()


def main():
    args = parse_args()
    # Default: no proxy. Pass --proxy <URL> to route through SOCKS5.
    proxy_url = args.proxy
    output = Path(args.output).resolve()
    use_color = args.color if args.color is not None else sys.stderr.isatty()
    log = Log(use_color=use_color, log_file=args.log_file)

    try:
        log.info("=== Steam Workshop Downloader v5 ===")
        log.dim(f"  AppID: {args.appid}, "
                f"IDs: {', '.join(str(w) for w in args.workshopid)}")
        log.dim(f"  Proxy: {proxy_url or '(direct connection)'}")
        log.dim(f"  Output: {output}")
        log.dim(f"  Retries per file: {args.retries}")

        client, cdn = _init_session(proxy_url, log)
        if client is None:
            log.err("Failed to initialize session")
            sys.exit(1)

        all_item_ids = _resolve_ids(client, args.appid, args.workshopid, log)
        total_items = len(all_item_ids)

        if total_items == 0:
            log.warn("No valid workshop items to download")
            try:
                client.logout()
            except Exception:
                pass
            sys.exit(1)

        log.info(f"\n{total_items} item{'s' if total_items > 1 else ''} to download")

        run_start = time.perf_counter()
        total_ok = total_fail = total_bytes = 0
        failed_items = 0

        with Progress(total_items=total_items, log=log, verbose=args.verbose) as prog:
            for idx, wid in enumerate(all_item_ids, 1):
                prog.start_item(idx, str(wid))
                stats = download_item(
                    cdn, args.appid, wid, output,
                    prog, log,
                    verbose=args.verbose, retries=args.retries,
                )
                prog.end_item()
                if stats is None or stats.fully_failed:
                    failed_items += 1
                else:
                    total_ok += stats.ok
                    total_fail += stats.fail
                    total_bytes += stats.bytes_done

        duration = time.perf_counter() - run_start
        try:
            client.logout()
        except Exception:
            pass

        # --- final summary ---
        log.blank()
        rule = "-" * 50
        log.info(rule)
        items_line = (f"  Items:  {total_items} total / "
                      f"{total_items - failed_items} ok / {failed_items} failed")
        log.info(items_line)
        files_line = (f"  Files:  {total_ok + total_fail} total / "
                      f"{total_ok} ok / {total_fail} failed  "
                      f"({_fmt_size(total_bytes)})")
        log.info(files_line)
        log.info(f"  Duration: {_fmt_duration(duration)}")
        if total_bytes > 0 and duration > 0:
            avg = (total_bytes / (1024 * 1024)) / duration
            log.info(f"  Average:  {avg:.1f} MB/s")
        else:
            log.info("  Average:  n/a")
        log.info(rule)

        if failed_items:
            sys.exit(1)
        log.info("All items downloaded.")
    finally:
        log.close()


if __name__ == "__main__":
    main()
