// Package chunkcodec decompresses post-AES Steam depot chunks, pure Go.
//
// Supported formats (auto-detected by magic), matching the steamclient64.dll
// dispatcher sub_138CEAA90:
//
//	VSZa  — "VSZa" + CRC + zstd frame(s) (Valve zstd magic 0xFD2FB525/26/27)
//	VZa   — "VZa"  + CRC + LZMA1 raw payload (legacy)
//	gzip  — 1F 8B
//	ZIP   — "PK"   (single-entry archive)
//	store — raw, no compression (no known magic, final fallback)
//
// VSZa was reverse-engineered from steamclient64.dll (see IDA notes): the
// payload after the 8-byte header is a standard zstd frame sequence whose
// frame magic uses Valve's renumbered constants (…25/26/27 instead of the
// upstream …28). No dictionary is used — the "zstd_dict_valve" string in
// the DLL is a red herring; all three decompression paths skip
// loadDictionary. We rewrite the magic to the standard value and decode
// with klauspost/compress.
package chunkcodec

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Format IDs mirror the dispatcher's *format out-param.
const (
	FormatUnknown = 0
	FormatStore   = 1
	FormatGzip    = 2
	FormatLZMA    = 3 // raw LZMA (not wired; Valve hasn't used it for years)
	FormatZip     = 4
	FormatVZa     = 5
	FormatVSZa    = 7
)

// acceptedZstdMagics are the frame magics the DLL's dispatcher accepts inside
// VSZa payloads. 0xFD2FB528 is upstream standard zstd; 0x25/0x26/0x27 are
// Valve's legacy variants (dispatcher branch: (magic + 47205083) <= 2, then
// the == 0xFD2FB528 case). Real depot chunks use the standard magic.
var acceptedZstdMagics = []uint32{0xFD2FB525, 0xFD2FB526, 0xFD2FB527, 0xFD2FB528}

const (
	vszaHeaderLen = 8  // "VSZa" + crc32
	vszaFooterLen = 15 // crc32 + size64 + "zsv"
	vzaHeaderLen  = 12 // "VZa" + crc32 + 5 props
	vzaFooterLen  = 10 // crc32 + size32 + "zv"
)

// Decompress auto-detects the chunk format and returns plaintext file data.
func Decompress(data []byte) ([]byte, error) {
	out, _, err := DecompressWithFormat(data)
	return out, err
}

// DecompressWithFormat additionally reports the detected Format* id.
func DecompressWithFormat(data []byte) ([]byte, int, error) {
	switch {
	case len(data) >= 4 && data[0] == 'V' && data[1] == 'S' && data[2] == 'Z' && data[3] == 'a':
		out, err := decompressVSZa(data)
		return out, FormatVSZa, err
	case len(data) >= 2 && data[0] == 'V' && data[1] == 'Z':
		out, err := decompressVZa(data)
		return out, FormatVZa, err
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		out, err := gunzip(data)
		return out, FormatGzip, err
	case len(data) >= 2 && data[0] == 'P' && data[1] == 'K':
		out, err := unzipFirst(data)
		return out, FormatZip, err
	default:
		// Store (uncompressed) chunks have no magic; dispatcher treats them
		// as passthrough when nothing else matches and size fits.
		if len(data) > 0 {
			out := make([]byte, len(data))
			copy(out, data)
			return out, FormatStore, nil
		}
		return nil, FormatUnknown, fmt.Errorf("empty chunk")
	}
}

// decompressVSZa handles "VSZa" containers: header(8) + zstd frames + footer(15).
func decompressVSZa(data []byte) ([]byte, error) {
	if len(data) < vszaHeaderLen+vszaFooterLen {
		return nil, fmt.Errorf("VSZa too short (%d bytes)", len(data))
	}
	if string(data[len(data)-3:]) != "zsv" {
		return nil, fmt.Errorf("VSZa bad footer magic %q", data[len(data)-3:])
	}
	wantCRC := binary.LittleEndian.Uint32(data[4:8])
	wantSize := binary.LittleEndian.Uint64(data[len(data)-11 : len(data)-3])

	payload := data[vszaHeaderLen : len(data)-vszaFooterLen]
	out, err := decodeValveZstd(payload, wantSize)
	if err != nil {
		return nil, err
	}
	if wantSize != 0 && uint64(len(out)) != wantSize {
		return nil, fmt.Errorf("VSZa size mismatch: got %d want %d", len(out), wantSize)
	}
	if crc32.ChecksumIEEE(out) != wantCRC {
		return nil, fmt.Errorf("VSZa CRC mismatch")
	}
	return out, nil
}

// decodeValveZstd decodes one or more concatenated zstd frames whose magic
// may be a Valve-renumbered variant, standard, or a skippable frame.
//
// Buffer-to-buffer semantics matching the DLL: the total decompressed size
// must equal wantSize (the VSZa container's declared original size). The DLL
// errors ZSTD_srcSize_wrong when the stream produces a different size, so we
// do too — DecodeAll's silent frame-header size would mask container damage.
func decodeValveZstd(payload []byte, wantSize uint64) ([]byte, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("zstd payload too short")
	}
	fixed := make([]byte, len(payload))
	copy(fixed, payload)
	magic := binary.LittleEndian.Uint32(fixed[0:4])
	switch magic {
	case 0xFD2FB528:
		// already standard — real depot chunks take this path.
	case acceptedZstdMagics[0], acceptedZstdMagics[1], acceptedZstdMagics[2]:
		// legacy Valve variant — rewrite to standard before decoding.
		binary.LittleEndian.PutUint32(fixed[0:4], 0xFD2FB528)
	default:
		if magic&0xFFFFFFF0 != 0x184D2A50 { // not a skippable frame either
			return nil, fmt.Errorf("not a zstd frame (magic=%08x)", magic)
		}
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	out, err := dec.DecodeAll(fixed, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decode: %w", err)
	}
	if uint64(len(out)) != wantSize {
		return nil, fmt.Errorf("zstd size mismatch: got %d want %d (srcSize_wrong)", len(out), wantSize)
	}
	return out, nil
}

// decompressVZa handles the legacy "VZa" container (LZMA1 raw payload).
func decompressVZa(data []byte) ([]byte, error) {
	if len(data) < vzaHeaderLen+vzaFooterLen {
		return nil, fmt.Errorf("VZa too short")
	}
	if data[len(data)-2] != 'z' || data[len(data)-1] != 'v' {
		return nil, fmt.Errorf("VZa bad footer")
	}
	props := data[7:12]
	payload := data[vzaHeaderLen : len(data)-vzaFooterLen]
	wantCRC := binary.LittleEndian.Uint32(data[len(data)-10 : len(data)-6])
	wantSize := binary.LittleEndian.Uint32(data[len(data)-6 : len(data)-2])

	out, err := decodeLZMA1Raw(props, payload, wantSize)
	if err != nil {
		return nil, err
	}
	if wantSize != 0 && uint32(len(out)) != wantSize {
		return nil, fmt.Errorf("VZa size mismatch: got %d want %d", len(out), wantSize)
	}
	if crc32.ChecksumIEEE(out) != wantCRC {
		return nil, fmt.Errorf("VZa CRC mismatch")
	}
	return out, nil
}

// decodeLZMA1Raw is implemented in vza_lzma.go (pure-Go LZMA1 decoder).
func decodeLZMA1Raw(props, payload []byte, wantSize uint32) ([]byte, error) {
	return lzma1Decode(props, payload, int64(wantSize))
}

func gunzip(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	return io.ReadAll(gr)
}

func unzipFirst(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) == 0 {
		return nil, fmt.Errorf("empty zip chunk")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
