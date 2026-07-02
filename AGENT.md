# AGENT.md — Codebase Map for AI Agents

## Overview

**Steam Workshop Downloader (`swd`)** — downloads Steam Workshop items
without launching `steamcmd.exe`. Uses `ValvePython/steam` for the Steam
protocol and `ctypes` to call `steamclient64.dll` for VSZa chunk
decompression. Packaged as a real installable Python package with
console script, tests, and CI.

## Repository layout

```
src/swd/             Production code
  cli.py             argparse + cmd_swd() — the entry point
  constants.py       RVA numbers, defaults, magic numbers
  utils.py           fmt_size, fmt_duration, compute_backoff
  dll/               ctypes wrapper around steamclient64.dll
    buffer.py        CUtlBuffer struct + enable_vt_on_windows
    loader.py        load_dll, decompress
  steam/             Steam-protocol layer (proxy, login, collections)
    proxy.py         parse_proxy_url (pure), setup_proxy (side-effect)
    patch.py         patch_cdn_client_get_chunk
    session.py       init_session → (client, cdn)
    workshop.py      resolve_ids (collection expansion)
  download/          Per-item download loop
    item.py          download_item + ItemStats
  ui/                Human output
    log.py           Colors, Log
    progress.py      Progress, ItemStats

tests/               pytest suite, all mock Steam and ctypes
  conftest.py        shared fixtures: FakeCDN, FakeFile, FakeManifest
  test_utils.py
  test_log.py
  test_progress.py
  test_proxy.py
  test_dll_smoke.py
  test_download_item.py
  test_cli.py

.github/workflows/ci.yml    ruff + mypy + pytest on Linux + Windows × py3.10-3.13
pyproject.toml              PEP 621 metadata, hatchling backend, scripts entry
LICENSE                     MIT
```

## Dependency direction

Single direction, no cycles:

```
cli  →  download  →  steam  →  valve-python/steam
              ↘
               dll  →  ctypes
              ↗
ui ←────────────┘    (used by download + cli; never imports them)
```

## Entry points

| How                                                | What runs                          |
|----------------------------------------------------|------------------------------------|
| `swd ...` (after `pip install .`)                  | `swd.cli:cmd_swd`                  |
| `python -m swd ...`                                | `swd.__main__` → `swd.cli.cmd_swd` |
| `python -c "import swd; print(swd.__version__)"`   | (just import)                      |
| `pytest`                                           | all tests under `tests/`           |

The `__init__.py` deliberately avoids importing `steam` so that
`import swd` is cheap and works even when `steam[client]` is not
installed (e.g. for linting or quick version checks).

## Key functions

| Function | Location | Purpose |
|---|---|---|
| `cmd_swd(argv)` | `swd/cli.py` | Top-level entry; returns exit code |
| `init_session(proxy_url, log)` | `swd/steam/session.py` | Login + CDNClient setup |
| `resolve_ids(client, app_id, ids, log)` | `swd/steam/workshop.py` | Expand collections recursively |
| `parse_proxy_url(url)` | `swd/steam/proxy.py` | Pure URL parsing (testable) |
| `setup_proxy(url)` | `swd/steam/proxy.py` | Configure `pysocks` globally |
| `patch_cdn_client_get_chunk()` | `swd/steam/patch.py` | Monkey-patch CDNClient decompression |
| `download_item(cdn, app_id, wid, out_dir, prog, log, ...)` | `swd/download/item.py` | Per-item loop with retry |
| `load_dll()` | `swd/dll/loader.py` | Idempotent DLL loader |
| `decompress(data)` | `swd/dll/loader.py` | Single-chunk decompress |
| `Log.create(...)` | `swd/ui/log.py` | TTY-aware logger factory |
| `Progress(total_items, log)` | `swd/ui/progress.py` | Nested tqdm bars |
| `ItemStats` | `swd/ui/progress.py` | Per-item stats dataclass |

## CLI flags

| Flag | Default | Purpose |
|------|---------|---------|
| positional `appid` | required | Steam app ID |
| positional `workshopid ...` | required | One or more Workshop IDs |
| `-o / --output` | `.` | Output directory |
| `-v / --verbose` | off | Print one line per file |
| `--proxy URL` | none | Proxy URL — `socks5://`, `socks4://`, `http(s)://`; bare `host:port` defaults to SOCKS5 |
| `--retries N` | 5 | Per-file retry count with exponential backoff |
| `--color` / `--no-color` | auto | Force or disable ANSI color (auto = TTY detection) |
| `--log-file PATH` | none | Tee all human output (ANSI stripped) to PATH |
| `--version` | — | Print version and exit |

All progress and logging go to **stderr**. Exit code is 0 on full
success, 1 on any failure (Steam login, all-failed item, etc.).

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
- Output goes through a CUtlBuffer (48-byte struct, see
  `swd.dll.buffer.CUtlBuffer`).
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
| Failed retry overwrites other files | Old code did `f = next(iter(manifest.iter_files()))`; fixed by indexing into the cached `files` list |

## DLL Dependencies

- `steamclient64.dll` (25 MB) — main decompression logic, from steamcmd install
- `tier0_s64.dll` — Steam tier0 runtime
- `vstdlib_s64.dll` — Steam standard library

The first is required at runtime (load via `swd.dll.loader.load_dll`).
Tests mock ctypes entirely; the real DLL is not needed for CI.

## Related Files

- `IDA_REVERSE_SUMMARY.md` — Full IDA analysis document (Chinese)
- `src/swd/` — Source code
- `tests/` — Pytest suite

## Steam API Endpoint Used

- `PublishedFile.GetDetails#1` — UM (User Messaging) protobuf call via `client.send_um_and_wait()`
- Fields needed: `file_type` (2=collection), `children`, `hcontent_file`, `consumer_appid`
- CDN chunk URL: `depot/<depot_id>/chunk/<sha_hex>`

## Testing locally

```bash
pip install -e ".[dev]"
pytest -q                       # all tests
pytest tests/test_proxy.py -v   # one file
pytest --cov=swd                # with coverage
```

DLL tests are isolated to `test_dll_smoke.py`; they fully mock `ctypes`
and never load a real DLL, so they run on Linux and macOS too.