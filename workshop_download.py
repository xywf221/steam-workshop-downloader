#!/usr/bin/env python3
"""
Steam Workshop Offline Downloader v5
=====================================
Pure Python Steam Workshop downloader.
Uses ValvePython/steam for Steam protocol.
Uses ctypes to call steamclient64.dll for VSZa chunk decompression.

Usage: pip install steam[client] pysocks
       python workshop_download.py <AppID> <WorkshopID> [<WorkshopID>...] [options]
       python workshop_download.py 294100 3683834622 3685058533
"""

import argparse
import ctypes
import lzma
import os
import struct
import sys
from binascii import crc32
from io import BytesIO
from pathlib import Path
from typing import Optional
from zipfile import ZipFile

PROXY = "socks5://192.168.7.1:1070"
_DLL = None
_DECOMPRESS_FN = None


def _load_dll():
    """Load steamclient64.dll and cache VSZa decompression function."""
    global _DLL, _DECOMPRESS_FN
    if _DECOMPRESS_FN is not None:
        return
    dll_path = Path(__file__).parent / "steamclient64.dll"
    if not dll_path.exists():
        raise RuntimeError(
            f"steamclient64.dll not found at {dll_path}. "
            "Required for VSZa chunk decompression."
        )
    # Add DLL directory to search path so dependencies (tier0_s64.dll,
    # vstdlib_s64.dll, etc.) can be found
    os.add_dll_directory(str(dll_path.parent))
    _DLL = ctypes.CDLL(str(dll_path))
    # RVA of sub_138E86360 (Steam custom decompression)
    fn_addr = _DLL._handle + 0xE86360
    _DECOMPRESS_FN = ctypes.CFUNCTYPE(
        ctypes.c_int,       # returned decompressed size
        ctypes.c_void_p,    # output buffer
        ctypes.c_int,       # expected decompressed size
        ctypes.c_void_p,    # compressed input
        ctypes.c_int        # compressed input size
    )(fn_addr)


def _vsza_decompress(data: bytes) -> bytes:
    """
    Decompress a VSZa-format chunk using steamclient64.dll.

    Format (from IDA analysis):
        [0:4]    "VSZa" magic
        [4:8]    CRC32 of original data
        [8:-15]  Compressed data (Steam custom algorithm)
        [-15:-11] CRC32 again
        [-11:-7]  Original size (low 32 bits)
        [-7:-3]   Original size (high 32 bits)
        [-3:]     "zsv" footer
    """
    _load_dll()
    if data[:4] != b"VSZa" or data[-3:] != b"zsv":
        raise ValueError("Not a valid VSZa chunk")
    expected_size = struct.unpack("<I", data[-11:-7])[0]
    expected_crc = struct.unpack("<I", data[-15:-11])[0]
    compressed = data[8:-15]

    output = ctypes.create_string_buffer(expected_size)
    result = _DECOMPRESS_FN(output, expected_size, compressed, len(compressed))

    if result != expected_size:
        raise RuntimeError(
            f"VSZa decompression failed: returned {result}, expected {expected_size}"
        )
    decompressed = output.raw[:expected_size]
    if crc32(decompressed) != expected_crc:
        raise RuntimeError("VSZa CRC mismatch")
    return decompressed


def setup_proxy(proxy_url: str = PROXY):
    import socks, socket
    p = proxy_url.replace("socks5://", "").replace("socks5h://", "")
    h, port = p.split(":")
    socks.set_default_proxy(socks.SOCKS5, h, int(port))
    socket.socket = socks.socksocket


def patch_vsza():
    """Monkey-patch CDNClient.get_chunk for VSZa/VZa format support."""
    from steam.client.cdn import CDNClient
    from steam.core.crypto import symmetric_decrypt
    from steam.exceptions import SteamError

    def vsza_get_chunk(self, app_id, depot_id, chunk_id):
        key = (depot_id, chunk_id)
        if key not in self._chunk_cache:
            resp = self.cdn_cmd("depot", "%s/chunk/%s" % (depot_id, chunk_id))
            data = symmetric_decrypt(resp.content, self.get_depot_key(app_id, depot_id))

            if data[:4] == b"VSZa":  # New VSZa format
                data = _vsza_decompress(data)

            elif data[:2] == b"VZ":  # Original VZa format
                if data[-2:] != b"zv":
                    raise SteamError("VZ: Invalid footer")
                if data[2:3] != b"a":
                    raise SteamError("VZ: Invalid version")
                lzma_filter = lzma._decode_filter_properties(
                    lzma.FILTER_LZMA1, data[7:12])
                checksum, dsz = struct.unpack("<II", data[-10:-2])
                result = lzma.LZMADecompressor(
                    lzma.FORMAT_RAW, filters=[lzma_filter]
                ).decompress(data[12:-9])[:dsz]
                if crc32(result) != checksum:
                    raise SteamError("VZ: CRC mismatch")
                data = result

            elif data[:2] == b"\x1f\x8b":  # gzip
                import zlib
                data = zlib.decompress(data, 15 + 32)

            else:
                with ZipFile(BytesIO(data)) as zf:
                    data = zf.read(zf.filelist[0])

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

    print("  [1/4] Connecting to Steam...")
    client = SteamClient()
    if client.anonymous_login() != 1:
        print("  [!] Login failed")
        return None, None
    print(f"  [v] Logged on ({client.steam_id})")

    print("  [2/4] Getting content servers...")
    cdn = CDNClient(client)
    print(f"  [v] Server: {cdn.get_content_server()}")
    return client, cdn


def download_item(cdn, app_id: int, workshop_id: int, output_dir: Path,
                  verbose: bool = False) -> Optional[Path]:
    """Download a single workshop item using an existing CDN session."""
    print(f"\n  --- Workshop {workshop_id} ---")
    print(f"  [3/4] Fetching manifest...")
    manifest = cdn.get_manifest_for_workshop_item(workshop_id)
    files = list(manifest.iter_files())
    print(f"  [v] '{manifest.name}' - {len(files)} files")

    print(f"  [4/4] Downloading files...")
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
                print(f"    [{i}/{len(files)}] {f.filename} ({len(data)} B)")
        except Exception as e:
            fail += 1
            if verbose:
                print(f"    [{i}/{len(files)}] {f.filename}: {e}")
        if not verbose and len(files) > 1:
            print(f"\r    Progress: {i}/{len(files)}", end="", flush=True)

    if not verbose:
        print()
    print(f"  [v] {ok}/{len(files)} files")

    total = sum(f.stat().st_size for f in out_dir.rglob("*") if f.is_file())
    cnt = sum(1 for f in out_dir.rglob("*") if f.is_file())
    print(f"  [v] {cnt} files, {total // 1024} KB -> {out_dir}")
    return out_dir if ok > 0 else None


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
    print(f"  AppID: {args.appid}, Workshop IDs: {', '.join(str(w) for w in args.workshopid)}")

    client, cdn = _init_session(proxy)
    if client is None:
        print("\n  [x] Failed to initialize session")
        sys.exit(1)

    fails = 0
    for wid in args.workshopid:
        result = download_item(cdn, args.appid, wid, output, args.verbose)
        if result is None:
            fails += 1

    try:
        client.logout()
    except:
        pass

    total = len(args.workshopid)
    ok = total - fails
    print(f"\n  [v] {ok}/{total} items downloaded")
    if fails:
        sys.exit(1)


if __name__ == "__main__":
    main()
