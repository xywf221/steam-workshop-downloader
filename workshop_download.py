#!/usr/bin/env python3
"""
Steam Workshop Offline Downloader v5
=====================================
Pure Python Steam Workshop downloader.
Uses ValvePython/steam for Steam protocol.
All chunk decompression delegated to steamclient64.dll via ctypes.

Usage: pip install steam[client] pysocks
       python workshop_download.py <AppID> <WorkshopID> [<WorkshopID>...] [options]
       python workshop_download.py 294100 3683834622 3685058533
       python workshop_download.py 294100 3047389309  (auto-expands collection)
"""

import argparse
import ctypes
import os
import struct
import sys
from binascii import crc32
from pathlib import Path
from typing import Optional

PROXY = "socks5://192.168.7.1:1070"

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


def setup_proxy(proxy_url: str = PROXY):
    import socks, socket
    p = proxy_url.replace("socks5://", "").replace("socks5h://", "")
    h, port = p.split(":")
    socks.set_default_proxy(socks.SOCKS5, h, int(port))
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


def _init_session(proxy: bool = True):
    """Initialize Steam session (login + CDN)."""
    if proxy:
        setup_proxy()
    patch_vsza()

    from steam.client import SteamClient
    from steam.client.cdn import CDNClient

    print("Connecting to Steam...")
    client = SteamClient()
    if client.anonymous_login() != 1:
        print("Login failed")
        return None, None
    print(f"  Logged on ({client.steam_id})")

    print("Getting content servers...")
    cdn = CDNClient(client)
    print(f"  Server: {cdn.get_content_server()}")
    return client, cdn


def _fmt_size(n: int) -> str:
    if n >= 1024 * 1024:
        return f"{n / 1024 / 1024:.1f} MB"
    if n >= 1024:
        return f"{n / 1024:.1f} KB"
    return f"{n} B"


def download_item(cdn, app_id: int, workshop_id: int, output_dir: Path,
                  verbose: bool = False) -> Optional[Path]:
    """Download a single workshop item using an existing CDN session."""
    print(f"=== {workshop_id} ===")
    print(f"  Fetching manifest...")
    manifest = cdn.get_manifest_for_workshop_item(workshop_id)
    files = list(manifest.iter_files())
    print(f"  '{manifest.name}' - {len(files)} files")

    print(f"  Downloading...")
    out_dir = output_dir / str(workshop_id)
    out_dir.mkdir(parents=True, exist_ok=True)

    ok = fail = 0
    for i, f in enumerate(files, 1):
        if not f.is_file:
            (out_dir / f.filename).mkdir(parents=True, exist_ok=True)
            continue
        path = out_dir / f.filename
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            data = f.read()
            with open(path, "wb") as fp:
                fp.write(data)
            ok += 1
            if verbose:
                print(f"    {f.filename}  {_fmt_size(len(data))}")
        except Exception as e:
            fail += 1
            if verbose:
                print(f"    {f.filename}  FAIL: {e}")
        if not verbose and len(files) > 1:
            print(f"\r    {i}/{len(files)}", end="", flush=True)

    if not verbose:
        print()

    total = sum(f.stat().st_size for f in out_dir.rglob("*") if f.is_file())
    cnt = sum(1 for f in out_dir.rglob("*") if f.is_file())
    print(f"  {_fmt_size(total)}  {cnt} files  ->  {out_dir}")
    return out_dir if ok > 0 else None


def _resolve_ids(client, app_id: int, ids) -> list:
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
                print(f"  Failed to get details for {wf.publishedfileid}, skipping")
                continue

            wid = wf.publishedfileid
            title = wf.title or str(wid)
            if wf.file_type == 2:  # Collection
                child_ids = [c.publishedfileid for c in wf.children]
                print(f"  '{title}' is a collection, {len(child_ids)} items")
                resolved.extend(_resolve_ids(client, app_id, child_ids))
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
    p.add_argument("--no-proxy", action="store_true")
    return p.parse_args()


def main():
    args = parse_args()
    proxy = not args.no_proxy
    output = Path(args.output).resolve()
    print(f"=== Steam Workshop Downloader v5 ===")
    print(f"  AppID: {args.appid}, IDs: {', '.join(str(w) for w in args.workshopid)}")

    client, cdn = _init_session(proxy)
    if client is None:
        print("Failed to initialize session")
        sys.exit(1)

    all_item_ids = _resolve_ids(client, args.appid, args.workshopid)
    total = len(all_item_ids)

    if total == 0:
        print("No valid workshop items to download")
        client.logout()
        sys.exit(1)

    print(f"\n{total} item{'s' if total > 1 else ''} to download")

    fails = 0
    for wid in all_item_ids:
        if download_item(cdn, args.appid, wid, output, args.verbose) is None:
            fails += 1

    try:
        client.logout()
    except:
        pass

    ok = total - fails
    if fails:
        print(f"\n{ok}/{total} downloaded, {fails} failed")
        sys.exit(1)
    else:
        print(f"\nAll {total} downloaded")


if __name__ == "__main__":
    main()
