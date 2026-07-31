package protocol

import (
	"encoding/binary"
	"fmt"
)

// Minimal protobuf wire helpers (enough for CMsgProtoBufHeader + ClientLogon).

const (
	wireVarint = 0
	wireBytes  = 2
	wireFixed32 = 5
	wireFixed64 = 1
)

// ProtoBufHeader is the subset of CMsgProtoBufHeader we need.
type ProtoBufHeader struct {
	SteamID         uint64
	ClientSessionID int32
	JobIDSource     uint64
	JobIDTarget     uint64
	TargetJobName   string
	EResult         int32
	ErrorMessage    string
}

// Serialize encodes as protobuf (field numbers match steammessages_base.proto).
// steamid / jobid_* are fixed64 on the wire (not varint).
func (h *ProtoBufHeader) Serialize() []byte {
	var b []byte
	if h.SteamID != 0 {
		b = appendFixed64Field(b, 1, h.SteamID)
	}
	if h.ClientSessionID != 0 {
		b = appendVarintField(b, 2, uint64(uint32(h.ClientSessionID)))
	}
	// Python defaults job ids to 0xFFFFFFFFFFFFFFFF; omit when still "none"
	// so we match sparse headers Valve accepts for ClientLogon.
	if h.JobIDSource != 0 && h.JobIDSource != ^uint64(0) {
		b = appendFixed64Field(b, 10, h.JobIDSource)
	}
	if h.JobIDTarget != 0 && h.JobIDTarget != ^uint64(0) {
		b = appendFixed64Field(b, 11, h.JobIDTarget)
	}
	if h.TargetJobName != "" {
		b = appendBytesField(b, 12, []byte(h.TargetJobName))
	}
	if h.EResult != 0 {
		b = appendVarintField(b, 13, uint64(uint32(h.EResult)))
	}
	if h.ErrorMessage != "" {
		b = appendBytesField(b, 14, []byte(h.ErrorMessage))
	}
	return b
}

// ParseProtoBufHeader parses a protobuf header blob.
func ParseProtoBufHeader(data []byte) (ProtoBufHeader, error) {
	var h ProtoBufHeader
	i := 0
	for i < len(data) {
		key, n := consumeVarint(data[i:])
		if n == 0 {
			return h, fmt.Errorf("bad protobuf key")
		}
		i += n
		field := int(key >> 3)
		wt := int(key & 7)
		switch wt {
		case wireVarint:
			v, n := consumeVarint(data[i:])
			if n == 0 {
				return h, fmt.Errorf("bad varint")
			}
			i += n
			switch field {
			case 2:
				h.ClientSessionID = int32(v)
			case 13:
				h.EResult = int32(v)
			}
		case wireBytes:
			l, n := consumeVarint(data[i:])
			if n == 0 || i+n+int(l) > len(data) {
				return h, fmt.Errorf("bad bytes len")
			}
			i += n
			val := data[i : i+int(l)]
			i += int(l)
			switch field {
			case 12:
				h.TargetJobName = string(val)
			case 14:
				h.ErrorMessage = string(val)
			}
		case wireFixed32:
			if i+4 > len(data) {
				return h, fmt.Errorf("fixed32 overflow")
			}
			i += 4
		case wireFixed64:
			if i+8 > len(data) {
				return h, fmt.Errorf("fixed64 overflow")
			}
			v := binary.LittleEndian.Uint64(data[i : i+8])
			i += 8
			switch field {
			case 1:
				h.SteamID = v
			case 10:
				h.JobIDSource = v
			case 11:
				h.JobIDTarget = v
			}
		default:
			return h, fmt.Errorf("unsupported wire type %d", wt)
		}
	}
	return h, nil
}

// MsgProto is a protobuf-bodied Steam message.
type MsgProto struct {
	Msg    EMsg
	Header ProtoBufHeader
	Body   []byte // already-serialized protobuf body
}

// Serialize encodes MsgHdrProtoBuf + body.
// Layout: <II protoBit(msg), len(protoHdr)> + protoHdr + body
func (m *MsgProto) Serialize() []byte {
	hdr := m.Header.Serialize()
	out := make([]byte, 8+len(hdr)+len(m.Body))
	binary.LittleEndian.PutUint32(out[0:4], SetProtoBit(m.Msg))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(hdr)))
	copy(out[8:], hdr)
	copy(out[8+len(hdr):], m.Body)
	return out
}

// ParseMsgProto parses a protobuf message from a decrypted packet.
func ParseMsgProto(data []byte) (*MsgProto, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("msgproto too short")
	}
	raw := binary.LittleEndian.Uint32(data[0:4])
	if !IsProto(raw) {
		return nil, fmt.Errorf("not a protobuf message")
	}
	msg := ClearProtoBit(raw)
	hlen := int(binary.LittleEndian.Uint32(data[4:8]))
	if 8+hlen > len(data) {
		return nil, fmt.Errorf("proto header length overflow")
	}
	hdr, err := ParseProtoBufHeader(data[8 : 8+hlen])
	if err != nil {
		return nil, err
	}
	return &MsgProto{
		Msg:    msg,
		Header: hdr,
		Body:   append([]byte(nil), data[8+hlen:]...),
	}, nil
}

// ClientLogonBody is the minimal CMsgClientLogon we send for anonymous login.
type ClientLogonBody struct {
	ProtocolVersion     uint32
	ClientPackageVersion uint32
}

// Serialize encodes CMsgClientLogon fields 1 and 5.
func (c ClientLogonBody) Serialize() []byte {
	var b []byte
	b = appendVarintField(b, 1, uint64(c.ProtocolVersion))
	b = appendVarintField(b, 5, uint64(c.ClientPackageVersion))
	return b
}

// ClientLogonResponseBody is the subset of CMsgClientLogonResponse we read.
type ClientLogonResponseBody struct {
	EResult           int32
	HeartbeatSeconds  int32
	AccountFlags      uint32
	CellID            uint32
}

// ParseClientLogonResponse parses the logon response body.
func ParseClientLogonResponse(data []byte) (ClientLogonResponseBody, error) {
	var r ClientLogonResponseBody
	i := 0
	for i < len(data) {
		key, n := consumeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		field := int(key >> 3)
		wt := int(key & 7)
		switch wt {
		case wireVarint:
			v, n := consumeVarint(data[i:])
			if n == 0 {
				return r, fmt.Errorf("bad varint")
			}
			i += n
			switch field {
			case 1:
				r.EResult = int32(v)
			case 3:
				r.HeartbeatSeconds = int32(v)
			case 6:
				r.AccountFlags = uint32(v)
			case 7:
				r.CellID = uint32(v)
			}
		case wireBytes:
			l, n := consumeVarint(data[i:])
			if n == 0 || i+n+int(l) > len(data) {
				return r, fmt.Errorf("bad bytes")
			}
			i += n + int(l)
		case wireFixed32:
			i += 4
		case wireFixed64:
			i += 8
		default:
			return r, fmt.Errorf("wire %d", wt)
		}
	}
	return r, nil
}

func appendVarintField(b []byte, field int, v uint64) []byte {
	b = appendVarint(b, uint64(field<<3|wireVarint))
	return appendVarint(b, v)
}

func appendFixed64Field(b []byte, field int, v uint64) []byte {
	b = appendVarint(b, uint64(field<<3|wireFixed64))
	tmp := make([]byte, 8)
	binary.LittleEndian.PutUint64(tmp, v)
	return append(b, tmp...)
}

func appendBytesField(b []byte, field int, v []byte) []byte {
	b = appendVarint(b, uint64(field<<3|wireBytes))
	b = appendVarint(b, uint64(len(v)))
	return append(b, v...)
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func consumeVarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if c < 0x80 {
			if i == 9 && c > 1 {
				return 0, 0
			}
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, 0
}
