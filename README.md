# Steam Workshop Downloader

Pure Python offline downloader for Steam Workshop items. No SteamCMD subprocess required.

```bash
pip install steam[client] pysocks tqdm
python workshop_download.py <AppID> <WorkshopID> [<WorkshopID>...]
```

## Features

- **No SteamCMD dependency** — pure Python with ctypes for chunk decompression
- **SOCKS5 proxy** support (optional via `--proxy`, direct by default)
- **Multiple Workshop IDs** per invocation — shared Steam session
- **Collection expansion** — auto-detects and expands collections (`file_type == 2`)
- **Multi-format support** — VSZa (ctypes DLL), VZa (pure LZMA), gzip, ZIP

## Requirements

- Python 3.8+
- `pip install steam[client] pysocks tqdm`
- Windows (required for `steamclient64.dll` ctypes call)

## Usage

```bash
# Single item
python workshop_download.py 294100 3683834622

# Multiple items (shared Steam session)
python workshop_download.py 294100 3683834622 3685058533

# Collection — auto-expands to all child items
python workshop_download.py 294100 3047389309

# Custom output directory
python workshop_download.py 294100 3683834622 -o ./downloads

# Verbose output (show each file as it downloads)
python workshop_download.py 294100 3683834622 -v

# Retry each failed file up to 10 times (default: 5)
python workshop_download.py 294100 3683834622 --retries 10

# Route outbound connections through a proxy (default: direct connection)
python workshop_download.py 294100 3683834622 --proxy socks5://127.0.0.1:1080
python workshop_download.py 294100 3683834622 --proxy http://user:pass@127.0.0.1:8080

# Pipe-friendly output (no ANSI escapes)
python workshop_download.py 294100 3683834622 --no-color > out.log 2>&1

# Tee a copy of the run log to a file
python workshop_download.py 294100 3683834622 --log-file run.log
```

## What you'll see

Every run prints progress to **stderr** (so piping stdout still works). On
a TTY you get a nested `tqdm` bar: an outer "items" bar and an inner
"files" bar, with stage prefixes, colored `OK`/`FAIL`/`retry` markers,
and a final summary block:

```
[INIT] Connecting to Steam...
OK  Logged on (anonymous)
[INIT] Getting content servers...
OK  Server: cdn-...
OK  'Mod Pack' - 3 files

=== Workshop 3683834622  (1/2) ===
OK  'Foo Mod' - 12 files
items   0%|          | 0/2 [00:00<?, ?item/s]
files  25%|████████▌| 3/12
-> retry: 1/5 on foo.bin: timeout (backoff 1s)
files 100%|██████████| 12/12
  45.3 MB  12 files (12 ok / 0 fail)  in 0:00:23  ->  ./3683834622

=== Workshop 3685058533  (2/2) ===
...
--------------------------------------------------
  Items:  2 total / 1 ok / 1 failed
  Files:  18 total / 12 ok / 6 failed  (48.7 MB)
  Duration: 0:01:23
  Average:  4.2 MB/s
--------------------------------------------------
```

When stderr is not a TTY (e.g. `> file.log` or `--no-color`), the tqdm
bars are skipped and you get plain text lines instead — same information,
no flicker.

## Proxy

By default the downloader connects **directly** (no proxy). To route
through a proxy, pass `--proxy <URL>`. The URL scheme selects the
protocol:

| Scheme                | Protocol         |
|-----------------------|------------------|
| `socks5://`, `socks5h://` (or bare `host:port`) | SOCKS5 |
| `socks4://`           | SOCKS4           |
| `http://`, `https://` | HTTP CONNECT     |

For HTTP/HTTPS proxies, `user:password@` in the URL is sent as the
proxy's basic auth.

```bash
# SOCKS5
python workshop_download.py 294100 3683834622 --proxy socks5://192.168.7.1:1070

# HTTP CONNECT with basic auth
python workshop_download.py 294100 3683834622 --proxy http://user:pass@proxy.example.com:8080
```

If `--proxy` is omitted, the downloader goes out directly.

## How It Works

```
Steam CM Protocol (anonymous login)
        ↓
Content Server Directory (discovery)
        ↓
PublishedFile.GetDetails (manifest info + collection detection)
        ↓
CDN Manifest Download (file list + chunk references)
        ↓
For each chunk:
  HTTP GET from CDN
  AES-256-CBC decrypt (depot key)
  Format detection → decompress
        ↓
Files written to disk
```

### Chunk Compression Formats

The decompression dispatcher (reversed from `steamclient64.dll`) handles 5 formats:

| Magic | Format | Decompression |
|-------|--------|--------------|
| `VSZa` | Steam custom (new) | `ctypes` → `steamclient64.dll` RVA `0xE86360` |
| `VZa` | LZMA custom props | Pure Python `lzma.FORMAT_RAW` |
| `1F 8B` | gzip | `zlib.decompress` |
| `PK 03 04` | ZIP (single-file) | `zipfile.ZipFile` |
| raw LZMA | LZMA standard | `lzma` library |

## Files

```
steam-workshop-downloader/
├── workshop_download.py    Main downloader script
├── steamclient64.dll       Steam client DLL (for VSZa decompression, 25 MB)
├── tier0_s64.dll           DLL dependency
├── vstdlib_s64.dll         DLL dependency
├── IDA_REVERSE_SUMMARY.md  Full reverse engineering documentation (Chinese)
├── README.md               This file
├── AGENT.md                AI agent context / codebase map
├── .gitattributes          Git LFS config for *.dll
└── .gitignore
```

## Reverse Engineering

See [IDA_REVERSE_SUMMARY.md](IDA_REVERSE_SUMMARY.md) for a complete walkthrough of the IDA analysis on `steamclient64.dll`, including:

- VZa and VSZa binary format structures
- Key function table (RVAs, sizes, purposes)
- Decompression dispatcher logic
- Pitfalls and wrong turns during analysis

## License

MIT
