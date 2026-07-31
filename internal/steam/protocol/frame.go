package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Magic is the Steam TCP packet magic "VT01".
var Magic = [4]byte{'V', 'T', '0', '1'}

// ReadPacket reads one length-prefixed VT01 frame from r.
func ReadPacket(r io.Reader) ([]byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[0:4])
	if hdr[4] != Magic[0] || hdr[5] != Magic[1] || hdr[6] != Magic[2] || hdr[7] != Magic[3] {
		return nil, fmt.Errorf("bad steam magic %q", hdr[4:])
	}
	if n > 16*1024*1024 {
		return nil, fmt.Errorf("steam packet too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WritePacket writes one VT01 frame to w.
func WritePacket(w io.Writer, payload []byte) error {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	copy(hdr[4:], Magic[:])
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
