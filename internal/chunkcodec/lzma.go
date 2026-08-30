package chunkcodec

// Pure-Go LZMA1 raw-stream decoding for Steam's legacy VZa chunks, backed by
// github.com/ulikunitz/xz/lzma. VZa stores a bare LZMA1 stream (no 13-byte
// classic header), so we synthesize the header (props byte + dictSize +
// uncompressed size) and hand the stream to the well-tested xz decoder.
//
// This replaced a hand-rolled range coder that diverged from the LZMA SDK on
// certain rep-match probability layouts (see commit history); using the
// upstream decoder eliminates that class of bug.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

// lzma1Decode decodes a raw LZMA1 stream. props is the 5-byte LZMA property
// block (1 props byte + 4-byte dictSize LE); payload is the compressed data;
// wantSize is the expected uncompressed size (from the VZa footer).
func lzma1Decode(props, payload []byte, wantSize int64) ([]byte, error) {
	if len(props) < 5 {
		return nil, fmt.Errorf("lzma props too short")
	}
	// Synthesize the 13-byte classic .lzma header the xz Reader expects:
	//   [0]    props byte (lc/lp/pb)
	//   [1:5]  dictSize LE
	//   [5:13] uncompressed size LE (int64)
	hdr := make([]byte, 13)
	hdr[0] = props[0]
	copy(hdr[1:5], props[1:5])
	size := wantSize
	if size < 0 {
		size = -1 // unknown
	}
	binary.LittleEndian.PutUint64(hdr[5:13], uint64(size))

	stream := make([]byte, 0, len(hdr)+len(payload))
	stream = append(stream, hdr...)
	stream = append(stream, payload...)

	r, err := lzma.NewReader(bytes.NewReader(stream))
	if err != nil {
		return nil, fmt.Errorf("lzma reader: %w", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lzma decode: %w", err)
	}
	return out, nil
}
