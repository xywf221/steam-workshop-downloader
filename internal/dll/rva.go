// Package dll loads steamclient64.dll and runs Valve's multi-format chunk
// decompression dispatcher (VSZa / VZa / gzip / ZIP / raw LZMA).
package dll

// RVAs inside steamclient64.dll (pinned to the DLL shipped with this repo).
// Re-derive via IDA if Steam updates the binary — see IDA_REVERSE_SUMMARY.md.
const (
	RVADecompressAll = 0xCEAA90
	RVACompressVSZa  = 0xCECC90
	RVAPutFunc       = 0xEB570
	RVAGetFunc       = 0xD3F20
	MaxChunkSize     = 100 * 1024 * 1024 // 100 MiB
)
