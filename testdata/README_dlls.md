# steamclient64.dll + friends — test oracle only

These DLLs are **not used at runtime**. Depot-chunk decompression is pure Go
(`internal/chunkcodec`, the `Decompressor` returned by `chunkcodec.Go()`).

They are kept here as a **test oracle**: tests load `steamclient64.dll` via
`dll.NewDecompressor("")` (which searches this directory) and require the Go
and DLL decompressors to agree byte-for-byte on DLL-produced data
(`TestDecompressorParity` in `internal/chunkcodec/crosscheck_test.go`).

Files:

- `steamclient64.dll` — Valve's multi-format chunk codec (VSZa/VZa/gzip/ZIP/LZMA)
- `tier0_s64.dll`, `vstdlib_s64.dll` — its load-time dependencies

If Steam ships an updated `steamclient64.dll` and chunk formats change, drop
the new DLL here and run:

```
go test ./internal/chunkcodec/ -run TestDecompressorParity -v
```

A mismatch means Valve changed something; re-derive the RVAs in
`internal/dll/rva.go` with IDA (see `IDA_REVERSE_SUMMARY.md`).
