# AGENT.md — Codebase Map for AI Agents

## Overview

**Steam Workshop Downloader (`swd`) 6.x** — Go rewrite. Downloads Steam
Workshop items without `steamcmd`. First-party Steam protocol (CM, UM, CDN);
Windows `steamclient64.dll` for VSZa/multi-format chunk decompress. Kong CLI.

Python 5.x is under `legacy/` only.

## Layout

```
cmd/swd/                 main → internal/cli
internal/
  cli/                   Kong: download, web (stub), version
  proxy/                 parse URL + SOCKS4/5/HTTP CONNECT dialer
  steam/
    cm/                  CM TCP, channel encrypt, anon login, jobs, SendUM
    crypto/              AES (ECB-IV+CBC) + RSA-OAEP-SHA1 universe key
    protocol/            VT01 frames, Msg/MsgProto headers
    pb/                  minimal protobuf wire codec
    workshop/            GetDetails, ResolveIDs (collections)
    cdn/                 content servers, depot key, manifest, chunks
    manifest/            depot manifest parse + filename decrypt
    session.go           InitSession façade
  dll/                   LoadLibrary + RVA decompress (Windows)
  chunkcodec/            DLL-first decompress
  download/              DownloadItem: skip SHA1, retries, jobs
  ui/                    Log (stderr, colour, log-file)
  util/                  backoff, SuggestJobs, FormatSize/Duration
  version/
scripts/cmlogin.go       login smoke test
legacy/                  archived Python 5.x
.github/workflows/go-ci.yml
steamclient64.dll        (+ tier0_s64, vstdlib_s64) runtime deps
```

## Dependency direction

```
cli → download → cdn → cm / workshop / manifest / crypto
         ↓
    chunkcodec → dll
cli → steam.InitSession → cm + proxy
```

## Entry points

| Command | Behaviour |
|---------|-----------|
| `swd download <appid> <ids…>` | Full download pipeline |
| `swd <appid> <ids…>` | Same (auto-inserts `download`) |
| `swd version` | Print version |
| `swd web` | Stub (phase 2) |
| `go test ./...` | Unit tests |

## Critical constants

| Name | Value |
|------|-------|
| Version | `6.0.0` |
| `RVADecompressAll` | `0xCEAA90` |
| `RVAPutFunc` / `RVAGetFunc` | `0xEB570` / `0xD3F20` |
| `MaxChunkSize` | 100 MiB |
| Anon login attempts | 5 |
| UM timeout / retries | 30s / 3 |
| Max auto jobs | 8 |
| Anon SteamID | `117093590311632896` |
| Client protocol / package | `65580` / `1561159470` |

## Protocol notes

- CM framing: `[u32 le len][VT01][payload]`; post-encrypt AES-HMAC session.
- `ClientLogon` header `steamid` is **fixed64**, not varint.
- UM: `EMsg.ServiceMethodCallFromClient` (151) + `header.target_job_name`;
  response matched on `jobid_target`.
- Workshop on SteamPipe: GetDetails → `hcontent_file` → manifest request code
  → `depot/{id}/manifest/{gid}/5/{code}` → chunks.
- Filenames in manifests often contain trailing NUL — strip before paths/logs.
- Proxy must allow **TCP :27017** to CM (HTTPS-only SOCKS fails channel encrypt).

## Do not

- Depend on go-steam / ValvePython at runtime or monkey-patch them.
- Delete `legacy/` without user request.
- Assume Linux can decompress VSZa without the DLL.
