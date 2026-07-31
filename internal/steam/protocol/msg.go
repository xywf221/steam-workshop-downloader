package protocol

import (
	"encoding/binary"
	"fmt"
)

// MsgHdr is the non-protobuf (struct) message header: msg + 2 job ids.
// Layout: <Iqq  (4+8+8 = 20 bytes).
type MsgHdr struct {
	Msg          EMsg
	TargetJobID  uint64
	SourceJobID  uint64
}

const msgHdrSize = 20

// Serialize encodes the header.
func (h *MsgHdr) Serialize() []byte {
	b := make([]byte, msgHdrSize)
	binary.LittleEndian.PutUint32(b[0:4], uint32(h.Msg))
	binary.LittleEndian.PutUint64(b[4:12], h.TargetJobID)
	binary.LittleEndian.PutUint64(b[12:20], h.SourceJobID)
	return b
}

// ParseMsgHdr parses a MsgHdr from data; returns header and body offset.
func ParseMsgHdr(data []byte) (MsgHdr, int, error) {
	if len(data) < msgHdrSize {
		return MsgHdr{}, 0, fmt.Errorf("msghdr too short: %d", len(data))
	}
	h := MsgHdr{
		Msg:         EMsg(binary.LittleEndian.Uint32(data[0:4])),
		TargetJobID: binary.LittleEndian.Uint64(data[4:12]),
		SourceJobID: binary.LittleEndian.Uint64(data[12:20]),
	}
	return h, msgHdrSize, nil
}

// Msg is a non-protobuf Steam message (used for channel encrypt).
type Msg struct {
	Header MsgHdr
	Body   []byte
}

// Serialize encodes header+body.
func (m *Msg) Serialize() []byte {
	return append(m.Header.Serialize(), m.Body...)
}

// ParseMsg parses a non-proto message.
func ParseMsg(data []byte) (*Msg, error) {
	h, off, err := ParseMsgHdr(data)
	if err != nil {
		return nil, err
	}
	return &Msg{Header: h, Body: append([]byte(nil), data[off:]...)}, nil
}

// --- Channel encrypt bodies (struct messages) ---

// ChannelEncryptRequest body.
type ChannelEncryptRequest struct {
	ProtocolVersion uint32
	Universe        uint32
	Challenge       []byte
}

// ParseChannelEncryptRequest parses the body after MsgHdr.
func ParseChannelEncryptRequest(body []byte) (ChannelEncryptRequest, error) {
	if len(body) < 8 {
		return ChannelEncryptRequest{}, fmt.Errorf("encrypt request too short")
	}
	r := ChannelEncryptRequest{
		ProtocolVersion: binary.LittleEndian.Uint32(body[0:4]),
		Universe:        binary.LittleEndian.Uint32(body[4:8]),
	}
	if len(body) > 8 {
		r.Challenge = append([]byte(nil), body[8:]...)
	}
	return r, nil
}

// ChannelEncryptResponse body (fixed 128-byte key slot).
type ChannelEncryptResponse struct {
	ProtocolVersion uint32
	KeySize         uint32
	Key             []byte // encrypted session key (≤128)
	CRC             uint32
}

// Serialize encodes the response body.
func (r ChannelEncryptResponse) Serialize() []byte {
	keySize := r.KeySize
	if keySize == 0 {
		keySize = 128
	}
	b := make([]byte, 8+128+8)
	binary.LittleEndian.PutUint32(b[0:4], r.ProtocolVersion)
	binary.LittleEndian.PutUint32(b[4:8], keySize)
	copy(b[8:8+128], r.Key)
	binary.LittleEndian.PutUint32(b[8+128:8+128+4], r.CRC)
	// last uint32 pad = 0
	return b
}

// ChannelEncryptResult body.
type ChannelEncryptResult struct {
	EResult uint32
}

// ParseChannelEncryptResult parses the body.
func ParseChannelEncryptResult(body []byte) (ChannelEncryptResult, error) {
	if len(body) < 4 {
		return ChannelEncryptResult{}, fmt.Errorf("encrypt result too short")
	}
	return ChannelEncryptResult{EResult: binary.LittleEndian.Uint32(body[0:4])}, nil
}
