# AGENT.md — Codebase Map for AI Agents

## Overview

**Steam Workshop Offline Downloader** — downloads Steam Workshop items without launching `steamcmd.exe`. Uses `ValvePython/steam` for Steam CM protocol and `ctypes` to call `steamclient64.dll` for VSZa chunk decompression.

## Architecture

```
_init_session(proxy_url, log)        # proxy + Steam login + CDNClient; all output via Log
    ↓
_resolve_ids(client, app_id, ids, log)  # PublishedFile.GetDetails → detect collections
    ↓                                     #   recurse into children, flatten
Progress(total_items, log)             # nested tqdm bars (items + files)
    ↓
download_item(cdn, app_id, wid, out_dir, progress, log)
    ├─ get_manifest_for_workshop_item()  # → CDNDepotManifest
    └─ for each file: f.read()
         └─ CDNClient.get_chunk()  # [PATCHED via patch_vsza()]
              ├─ HTTP GET depot/<id>/chunk/<sha>
              ├─ symmetric_decrypt (AES-256-CBC, depot key)
              └─ format detection → decompress
                   ├─ VSZa → _dll_decompress() → ctypes DLL call (RVA 0xCEAA90)
                   ├─ VZa  → handled by the same DLL dispatcher
                   ├─ gzip → handled by the same DLL dispatcher
                   └─ ZIP  → handled by the same DLL dispatcher
```

## Key Functions

| Function | Purpose |
|----------|---------|
| `_load_dll()` | Load `steamclient64.dll`, resolve RVA `0xCEAA90` (multi-format dispatcher) via `ctypes.CFUNCTYPE` |
| `_dll_decompress(data)` | Call DLL dispatcher with a CUtlBuffer output; auto-detects VSZa / VZa / gzip / ZIP / raw LZMA |
| `patch_vsza()` | Monkey-patch `CDNClient.get_chunk` to route all decompression through `_dll_decompress` |
| `_resolve_ids(client, app_id, ids, log)` | Query Steam API to detect collections, flatten recursively; `log` is required |
| `download_item(cdn, app_id, wid, out_dir, progress, log, verbose, retries)` | Download one item's files; returns `ItemStats` (None if no output dir) |
| `_init_session(proxy_url, log)` | Login + CDN setup; `log` is required |
| `Log` | Structured stderr output with `[STAGE]` prefix and ANSI color (auto-off when not a TTY) |
| `Progress` | Two nested `tqdm` bars; bars are `None` when stderr is not a TTY |
| `ItemStats` | Dataclass returned by `download_item` (out_dir, ok, fail, bytes_done, duration, name) |
| `_fmt_size(n)` | Human-readable file size (B/KB/MB) |
| `_fmt_duration(s)` | Human-readable duration (H:MM:SS) |

## CLI flags

| Flag | Default | Purpose |
|------|---------|---------|
| `-o / --output` | `.` | Output directory |
| `-v / --verbose` | off | Print one line per file (in addition to the bar) |
| `--proxy URL` | none | Proxy URL — `socks5://`, `socks4://`, `http(s)://`; bare `host:port` defaults to SOCKS5 |
| `--retries N` | 5 | Per-file retry count with exponential backoff |
| `--color` / `--no-color` | auto | Force or disable ANSI color (auto = TTY detection) |
| `--log-file PATH` | none | Tee all human output (ANSI stripped) to PATH |

All progress and logging go to **stderr**; only the final summary line
(`All items downloaded.` / `sys.exit(1)`) cares about exit code.

## VSZa Decompression (the ctypes trick)

```python
dll = ctypes.CDLL("steamclient64.dll")
DECOMPRESS_FN = ctypes.CFUNCTYPE(
    ctypes.c_int,       # return code (1=ok, 2=err, 25=buf_too_small, 53=crc)
    ctypes.c_void_p,    # rcx: input data
    ctypes.c_int,       # rdx: input size
    ctypes.c_void_p,    # r8:  CUtlBuffer* (output)
    ctypes.c_int,       # r9:  max output size
    ctypes.c_void_p,    # stack: out_format (optional)
)(dll._handle + 0xCEAA90)  # RVA of sub_138CEAA90 (multi-format dispatcher)
```

- **RVA `0xCEAA90`** is the multi-format chunk decompression dispatcher
  (`sub_138CEAA90` from IDA — supersedes the older `0xE86360` VSZa-only call)
- The dispatcher auto-detects VSZa / VZa / gzip / ZIP / raw LZMA from the
  first few bytes of the input; callers no longer need to dispatch themselves.
- Output goes through a CUtlBuffer (48-byte struct, see `_CUtlBuffer`).
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
- `workshop_download.py` — Main script (single-file, ~570 lines)

## Steam API Endpoint Used

- `PublishedFile.GetDetails#1` — UM (User Messaging) protobuf call via `client.send_um_and_wait()`
- Fields needed: `file_type` (2=collection), `children`, `hcontent_file`, `consumer_appid`
- CDN chunk URL: `depot/<depot_id>/chunk/<sha_hex>`
