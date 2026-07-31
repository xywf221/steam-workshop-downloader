# Steam Workshop Downloader

Offline Steam Workshop downloader. No `steamcmd` subprocess.

**Go 6.x** — first-party Steam protocol (CM / UM / CDN), [Kong](https://github.com/alecthomas/kong) CLI, Windows `steamclient64.dll` for VSZa, embedded **web UI**. **No ValvePython, no monkey-patches.**

Python 5.x sources are archived under [`legacy/`](legacy/) for reference.

## Build

```bash
go build -o swd.exe ./cmd/swd
# or: go install ./cmd/swd
```

Place next to the binary (from a steamcmd install):

- `steamclient64.dll`
- `tier0_s64.dll`
- `vstdlib_s64.dll`

Or set `SWD_STEAMCLIENT_DLL` to the full path of `steamclient64.dll`.

**Windows** is required for modern (VSZa) workshop chunks.

## Usage

```bash
swd version

# Single / multiple items
swd download 294100 3683834622
swd download 294100 3683834622 3685058533 -o ./downloads

# Collections auto-expand
swd download 294100 3047389309

# Proxy (must allow full TCP to Steam CM :27017 — not HTTPS-only)
swd download 294100 3683834622 --proxy socks5://127.0.0.1:1080
swd download 294100 3683834622 --proxy socks5://user:pass@host:1080
swd download 294100 3683834622 --proxy http://user:pass@proxy:8080

# Parallelism, retries, logging
swd download 294100 3683834622 -j 4 --retries 10 -v --log-file run.log --no-color

# Legacy positional form (auto-inserts "download")
swd 294100 3683834622
```

| Flag | Default | Meaning |
|------|---------|---------|
| `-o / --output` | `.` | Output root (`{out}/{workshopid}/…`) |
| `-v / --verbose` | off | Per-file lines |
| `--proxy` | none | `socks5://` `socks4://` `http(s)://` or `host:port` |
| `--retries` | 5 | Per-file attempts |
| `-j / --jobs` | 0 | Parallel files per item (`0` = auto, cap 8) |
| `--color` / `--no-color` | auto TTY | ANSI |
| `--log-file` | none | Tee (ANSI stripped) |

Exit `0` on full success, `1` on failure.

### Web UI

```bash
swd web --host 127.0.0.1 --port 8765 -o ./downloads
swd web --proxy socks5://127.0.0.1:1080 -o ./downloads
```

Open `http://127.0.0.1:8765` — browse / search by AppID, item detail, queue downloads (progress on `/downloads`). With `--proxy`, preview images are fetched server-side via `/media/image` so the browser does not need the proxy.

| Flag | Default | Meaning |
|------|---------|---------|
| `--host` | `127.0.0.1` | Bind address |
| `--port` | `8765` | Bind port |
| `-o / --output` | `.` | Directory for web-queued downloads |
| `--proxy` | none | Same schemes as `swd download` |

## Features

- Anonymous Steam login (multi-CM, channel encrypt)
- Collection expansion (`PublishedFile.GetDetails`)
- SteamPipe CDN manifests + chunk download (AES + multi-format decompress)
- Skip if local size + SHA-1 match
- Per-file retry with exponential backoff
- SOCKS5/4 and HTTP CONNECT proxies (credentials supported)
- Structured stderr logging
- Web UI: QueryFiles browse/search, media proxy, download queue

## Development

```bash
go test ./...
go run ./scripts/cmlogin.go                          # login smoke
go run ./scripts/cmlogin.go 'socks5://127.0.0.1:1080'
```

CI: [`.github/workflows/go-ci.yml`](.github/workflows/go-ci.yml).

Layout: see [`AGENT.md`](AGENT.md). DLL reverse notes: [`IDA_REVERSE_SUMMARY.md`](IDA_REVERSE_SUMMARY.md).

## License

MIT — see [`LICENSE`](LICENSE).
