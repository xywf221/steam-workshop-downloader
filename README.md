# Steam Workshop Downloader

Pure-Python offline downloader for Steam Workshop items. No `steamcmd`
subprocess — talks the Steam protocol directly via [`steam`][steam-pypi]
and uses `ctypes` against `steamclient64.dll` for chunk decompression.

```bash
pip install swd
swd 294100 3683834622
```

> Package name on PyPI: `swd`. Console command: `swd`. Importable as
> `import swd`. (Internal package name reflects the project alias while
> leaving the GitHub repo untouched.)

[steam-pypi]: https://pypi.org/project/steam/

## Features

- **No SteamCMD** — pure Python with ctypes for chunk decompression
- **Pluggable proxy** — `socks5://`, `socks4://`, or `http(s)://` CONNECT, via `pysocks`
- **Multiple Workshop IDs** per invocation — shared Steam session, batch progress
- **Collection expansion** — auto-detects and expands collections (`file_type == 2`)
- **Multi-format decompression** — VSZa, VZa, gzip, ZIP, raw LZMA, all dispatched by `steamclient64.dll`
- **Real progress bars** — nested `tqdm` bars (items × files) with ETA + speed
- **Structured logging** — stage-prefixed, ANSI-coloured, file-teeable
- **Per-file retry** — exponential backoff, configurable count
- **Installable as a package** — `pip install`, console script, real entry points
- **Tested & linted in CI** — ruff + mypy + pytest matrix on Linux and Windows

## Requirements

- Python **3.10+**
- `pip install swd` pulls in the runtime deps (`steam[client]`, `PySocks`, `tqdm`)
- **Windows** is required at runtime — `steamclient64.dll` is loaded via ctypes

The Windows DLLs (`steamclient64.dll`, `tier0_s64.dll`, `vstdlib_s64.dll`) come
from a `steamcmd` install. Place `steamclient64.dll` next to `pyproject.toml`
(the repository root when developing). Tests mock the DLL, so CI does not need
it.

## Installation

```bash
pip install swd
```

Or, for development:

```bash
git clone https://github.com/xywf221/steam-workshop-downloader
cd steam-workshop-downloader
pip install -e ".[dev]"
```

## Usage

```bash
# Single item
swd 294100 3683834622

# Multiple items (shared Steam session)
swd 294100 3683834622 3685058533

# Collection — auto-expands to all child items
swd 294100 3047389309

# Custom output directory
swd 294100 3683834622 -o ./downloads

# Verbose output (per-file lines under the bar)
swd 294100 3683834622 -v

# Retry each failed file up to 10 times (default: 5)
swd 294100 3683834622 --retries 10

# Route outbound connections through a proxy (default: direct)
swd 294100 3683834622 --proxy socks5://127.0.0.1:1080
swd 294100 3683834622 --proxy http://user:pass@proxy:8080

# Pipe-friendly output (no ANSI)
swd 294100 3683834622 --no-color > out.log 2>&1

# Tee a copy of the run log to a file
swd 294100 3683834622 --log-file run.log
```

`swd --help` lists every flag.

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

When stderr is not a TTY (e.g. `> file.log` or `--no-color`), the tqdm bars
are skipped and you get plain text lines instead — same information, no
flicker.

## Proxy

By default the downloader connects **directly** (no proxy). To route
through a proxy, pass `--proxy <URL>`. The URL scheme selects the
protocol:

| Scheme                                   | Protocol         |
|------------------------------------------|------------------|
| `socks5://`, `socks5h://` (or bare `host:port`) | SOCKS5          |
| `socks4://`                              | SOCKS4           |
| `http://`, `https://`                    | HTTP CONNECT     |

For HTTP/HTTPS proxies, `user:password@` in the URL is sent as basic auth.

## Project layout

```
steam-workshop-downloader/
├── pyproject.toml
├── README.md
├── AGENT.md
├── LICENSE
├── steamclient64.dll        # required at runtime (Windows)
├── src/swd/
│   ├── __init__.py
│   ├── __main__.py          # python -m swd
│   ├── cli.py               # argparse + cmd_swd() entry point
│   ├── constants.py         # RVA numbers, batch sizes, defaults
│   ├── utils.py             # fmt_size, fmt_duration, compute_backoff
│   ├── dll/
│   │   ├── buffer.py        # CUtlBuffer struct + enable_vt_on_windows
│   │   └── loader.py        # load_dll, decompress
│   ├── steam/
│   │   ├── proxy.py         # parse_proxy_url, setup_proxy
│   │   ├── patch.py         # patch_cdn_client_get_chunk
│   │   ├── session.py       # init_session
│   │   └── workshop.py      # resolve_ids (collection expansion)
│   ├── download/
│   │   └── item.py          # download_item, ItemStats
│   └── ui/
│       ├── log.py           # Colors, Log
│       └── progress.py      # Progress, ItemStats
└── tests/
    ├── conftest.py
    ├── test_utils.py
    ├── test_log.py
    ├── test_progress.py
    ├── test_proxy.py
    ├── test_dll_smoke.py
    ├── test_download_item.py
    └── test_cli.py
```

Module dependency direction is strictly one-way:

```
cli → download → steam → valve-python/steam
              ↘
               dll → ctypes
              ↗
ui ←────────────┘  (used by download + cli, never imports them)
```

## Development

```bash
pip install -e ".[dev]"
pytest -q
ruff check src tests
mypy src
```

CI runs on every push and pull request:

- `ubuntu-latest` + `windows-latest`
- Python 3.10, 3.11, 3.12, 3.13
- ruff, mypy, pytest with coverage

See `.github/workflows/ci.yml` for the matrix.

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
  Format detection → decompress (via steamclient64.dll)
        ↓
Files written to disk
```

### Chunk Compression Formats

The decompression dispatcher (reversed from `steamclient64.dll` at RVA
`0xCEAA90`) auto-detects and handles 5 formats:

| Magic     | Format             | Decompression                       |
|-----------|--------------------|-------------------------------------|
| `VSZa`    | Steam custom (new) | `ctypes` → `steamclient64.dll`      |
| `VZa`     | LZMA custom props  | Same dispatcher                     |
| `1F 8B`   | gzip               | Same dispatcher                     |
| `PK 03 04`| ZIP (single-file)  | Same dispatcher                     |
| raw LZMA  | LZMA standard      | Same dispatcher                     |

## Reverse Engineering

See [IDA_REVERSE_SUMMARY.md](IDA_REVERSE_SUMMARY.md) for a complete walkthrough of the IDA analysis on `steamclient64.dll`, including:

- VZa and VSZa binary format structures
- Key function table (RVAs, sizes, purposes)
- Decompression dispatcher logic
- Pitfalls and wrong turns during analysis

## License

MIT — see [LICENSE](LICENSE).