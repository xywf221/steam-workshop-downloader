# AGENT.md — Codebase Map for AI Agents

## Overview

**Steam Workshop Offline Downloader** — downloads Steam Workshop items without launching `steamcmd.exe`. Uses `ValvePython/steam` for Steam CM protocol and `ctypes` to call `steamclient64.dll` for VSZa chunk decompression.

## Architecture

```
_init_session()          # SOCKS5 proxy + Steam anonymous login + CDNClient
    ↓
_resolve_ids()           # PublishedFile.GetDetails → detect collections (file_type==2)
    ↓                     #   recurse into children, flatten to item ID list
download_item()          # per-item:
    ├─ get_manifest_for_workshop_item()  # → CDNDepotManifest
    └─ for each file: f.read()
         └─ CDNClient.get_chunk()  # [PATCHED via patch_vsza()]
              ├─ HTTP GET depot/<id>/chunk/<sha>
              ├─ symmetric_decrypt (AES-256-CBC, depot key)
              └─ format detection → decompress
                   ├─ VSZa → _vsza_decompress() → ctypes DLL call
                   ├─ VZa  → lzma.FORMAT_RAW + custom properties
                   ├─ gzip → zlib.decompress
                   └─ ZIP  → ZipFile
```

## Key Functions

| Function | Purpose |
|----------|---------|
| `_load_dll()` | Load `steamclient64.dll`, resolve RVA `0xE86360` via `ctypes.CFUNCTYPE` |
| `_vsza_decompress(data)` | Parse VSZa header/footer, call DLL, verify CRC32 |
| `patch_vsza()` | Monkey-patch `CDNClient.get_chunk` to add VSZa/VZa/gzip/ZIP handling |
| `_resolve_ids(client, app_id, ids)` | Query Steam API to detect collections, flatten recursively |
| `download_item(cdn, app_id, wid, out_dir)` | Download one item's files from manifest |
| `_init_session(proxy)` | Login + CDN setup |
| `_fmt_size(n)` | Human-readable file size (B/KB/MB) |

## VSZa Decompression (the ctypes trick)

```python
dll = ctypes.CDLL("steamclient64.dll")
DECOMPRESS_FN = ctypes.CFUNCTYPE(
    ctypes.c_int,       # returned decompressed size
    ctypes.c_void_p,    # output buffer
    ctypes.c_int,       # expected size
    ctypes.c_void_p,    # compressed input
    ctypes.c_int        # compressed input size
)(dll._handle + 0xE86360)  # RVA of sub_138E86360
```

- **RVA `0xE86360`** is the VSZa decompression function (`sub_138E86360` from IDA)
- Plain buffer-to-buffer API — no CUtlBuffer or Steam runtime structs needed
- DLL dependencies (`tier0_s64.dll`, `vstdlib_s64.dll`) must be in the search path
- `os.add_dll_directory()` is called before `ctypes.CDLL()`

## VSZa Format (from IDA)

```
[0:4]    "VSZa" magic
[4:8]    CRC32 (original data)
[8:-15]  Steam custom compressed data  ← NOT LZMA
[-15:-11] CRC32 (duplicate)
[-11:-7]  Original size (u32 LE)
[-7:-3]   Original size high (u32 LE, 0 for <4GB)
[-3:]     "zsv" footer
```

## VZa Format (pure Python LZMA)

```
[0:3]    "VZa" magic
[3:7]    CRC32
[7:12]   LZMA1 properties (5 bytes)
[12:-10] LZMA compressed data
[-10:-6] CRC32
[-6:-2]  Original size (u32 LE)
[-2:]    "zv" footer
```

## Decompression Dispatcher (from sub_138CEAA90)

Order matters — checked in sequence:

1. `0x1F 0x8B` → gzip (format 2)
2. `VZ` → VZa (format 5)
3. `VSZ` → VSZa (format 7)
4. `PK\x03\x04` → ZIP (format 4)
5. `byte0 & 0xF == 8 && LE16 % 31 == 0` → raw LZMA (format 3)

## Common Pitfalls

| Pitfall | Solution |
|---------|----------|
| `steamclient64.dll` fails to load | `os.add_dll_directory()` for `tier0_s64.dll` / `vstdlib_s64.dll` |
| Works with wrong size | Old VSZa parser tried LZMA — the format uses a Steam custom algorithm, not LZMA |
| "Failed getting workshop file info" | Usually wrong WorkshopID or private/inaccessible item |
| Collection not expanded | Ensure `includechildren: true` in `PublishedFile.GetDetails` request |

## DLL Dependencies

- `steamclient64.dll` (25 MB) — main decompression logic, from steamcmd install
- `tier0_s64.dll` — Steam tier0 runtime
- `vstdlib_s64.dll` — Steam standard library

All three are tracked via Git LFS (`*.dll filter=lfs diff=lfs merge=lfs -text`).

## Related Files

- `IDA_REVERSE_SUMMARY.md` — Full IDA analysis document (Chinese)
- `workshop_download.py` — Main script (314 lines)

## Steam API Endpoint Used

- `PublishedFile.GetDetails#1` — UM (User Messaging) protobuf call via `client.send_um_and_wait()`
- Fields needed: `file_type` (2=collection), `children`, `hcontent_file`, `consumer_appid`
- CDN chunk URL: `depot/<depot_id>/chunk/<sha_hex>`
