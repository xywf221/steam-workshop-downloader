# Steam Workshop Downloader

Pure Python offline downloader for Steam Workshop items. No SteamCMD subprocess required.

```bash
pip install steam[client] pysocks
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
- `pip install steam[client] pysocks`
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

# Route outbound connections through a SOCKS5 proxy (default: direct connection)
python workshop_download.py 294100 3683834622 --proxy socks5://127.0.0.1:1080
```

## Proxy

By default the downloader connects **directly** (no proxy). To route
through a SOCKS5 proxy, pass `--proxy <URL>`:

```bash
python workshop_download.py 294100 3683834622 --proxy socks5://192.168.7.1:1070
```

The URL accepts `socks5://host:port`, `socks5h://host:port`, or bare
`host:port`. If `--proxy` is omitted, the downloader goes out directly.

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
