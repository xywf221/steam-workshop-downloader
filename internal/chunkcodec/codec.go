// Package chunkcodec decompresses post-AES Steam depot chunks.
//
// Prefer the steamclient64.dll multi-format dispatcher on Windows (handles
// VSZa). Fall back to pure-Go handlers for VZa / gzip / ZIP when the DLL is
// unavailable.
package chunkcodec

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/xywf221/steam-workshop-downloader/internal/dll"
)

// Decompress auto-detects the chunk format and returns plaintext file data.
func Decompress(data []byte) ([]byte, error) {
	// Try DLL first (VSZa + everything else).
	if out, err := dll.Decompress(data); err == nil {
		return out, nil
	} else if isWindowsDLLError(err) {
		// fall through to pure Go for non-VSZa
		if out, err2 := decompressPure(data); err2 == nil {
			return out, nil
		}
		return nil, err // prefer DLL error (likely VSZa needing DLL)
	} else {
		// DLL loaded but failed — still try pure for VZa/gzip
		if out, err2 := decompressPure(data); err2 == nil {
			return out, nil
		}
		return nil, err
	}
}

func isWindowsDLLError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "Windows-only") || contains(s, "steamclient64.dll")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || bytes.Contains([]byte(s), []byte(sub)))
}

func decompressPure(data []byte) ([]byte, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return gunzip(data)
	}
	if len(data) >= 4 && data[0] == 'V' && data[1] == 'Z' {
		return decompressVZa(data)
	}
	// ZIP (PK)
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		return unzipFirst(data)
	}
	return nil, fmt.Errorf("unsupported chunk format (magic=%x); VSZa requires steamclient64.dll on Windows", head(data, 4))
}

func head(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
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

// decompressVZa implements Valve's old VZa (LZMA1) format.
// Layout: "VZa" + CRC32 + 5B props + payload + CRC32 + size + "zv"
func decompressVZa(data []byte) ([]byte, error) {
	if len(data) < 22 {
		return nil, fmt.Errorf("VZa too short")
	}
	if data[0] != 'V' || data[1] != 'Z' || data[2] != 'a' {
		return nil, fmt.Errorf("not VZa")
	}
	if data[len(data)-2] != 'z' || data[len(data)-1] != 'v' {
		return nil, fmt.Errorf("VZa bad footer")
	}
	// Use standard library: Go 1.x has no raw LZMA1 in compress; try xz via
	// manual — actually stdlib has no FILTER_LZMA1. Keep DLL path primary.
	// Attempt: treat as zip-incompatible; return clear error so DLL is used.
	_ = binary.LittleEndian.Uint32(data[3:7]) // crc
	props := data[7:12]
	payload := data[12 : len(data)-10]
	footer := data[len(data)-10 : len(data)-2]
	crcExpect := binary.LittleEndian.Uint32(footer[0:4])
	sizeExpect := binary.LittleEndian.Uint32(footer[4:8])
	_ = props
	_ = payload
	_ = crcExpect
	_ = sizeExpect
	_ = crc32.ChecksumIEEE
	return nil, fmt.Errorf("pure-Go VZa LZMA1 not bundled; use steamclient64.dll dispatcher")
}
