// Package pb provides minimal protobuf wire encode/decode helpers for Steam messages.
package pb

import (
	"encoding/binary"
	"fmt"
)

const (
	WireVarint  = 0
	WireFixed64 = 1
	WireBytes   = 2
	WireFixed32 = 5
)

// Encoder accumulates protobuf fields.
type Encoder struct {
	b []byte
}

// Bytes returns the encoded buffer.
func (e *Encoder) Bytes() []byte { return e.b }

// VarintField appends a varint field.
func (e *Encoder) VarintField(field int, v uint64) {
	e.b = appendVarint(e.b, uint64(field<<3|WireVarint))
	e.b = appendVarint(e.b, v)
}

// BoolField appends a bool as varint 0/1.
func (e *Encoder) BoolField(field int, v bool) {
	if v {
		e.VarintField(field, 1)
	} else {
		e.VarintField(field, 0)
	}
}

// Fixed64Field appends a fixed64 field.
func (e *Encoder) Fixed64Field(field int, v uint64) {
	e.b = appendVarint(e.b, uint64(field<<3|WireFixed64))
	tmp := make([]byte, 8)
	binary.LittleEndian.PutUint64(tmp, v)
	e.b = append(e.b, tmp...)
}

// Fixed32Field appends a fixed32 field.
func (e *Encoder) Fixed32Field(field int, v uint32) {
	e.b = appendVarint(e.b, uint64(field<<3|WireFixed32))
	tmp := make([]byte, 4)
	binary.LittleEndian.PutUint32(tmp, v)
	e.b = append(e.b, tmp...)
}

// BytesField appends length-delimited bytes.
func (e *Encoder) BytesField(field int, v []byte) {
	e.b = appendVarint(e.b, uint64(field<<3|WireBytes))
	e.b = appendVarint(e.b, uint64(len(v)))
	e.b = append(e.b, v...)
}

// StringField appends a string field.
func (e *Encoder) StringField(field int, v string) {
	e.BytesField(field, []byte(v))
}

// MessageField appends an embedded message.
func (e *Encoder) MessageField(field int, msg []byte) {
	e.BytesField(field, msg)
}

// Field is one decoded protobuf field.
type Field struct {
	Number int
	Wire   int
	Varint uint64
	Fixed32 uint32
	Fixed64 uint64
	Bytes  []byte
}

// Decode walks all fields in data.
func Decode(data []byte) ([]Field, error) {
	var out []Field
	i := 0
	for i < len(data) {
		key, n := ConsumeVarint(data[i:])
		if n == 0 {
			return out, fmt.Errorf("bad protobuf key at %d", i)
		}
		i += n
		num := int(key >> 3)
		wt := int(key & 7)
		f := Field{Number: num, Wire: wt}
		switch wt {
		case WireVarint:
			v, n := ConsumeVarint(data[i:])
			if n == 0 {
				return out, fmt.Errorf("bad varint")
			}
			i += n
			f.Varint = v
		case WireFixed64:
			if i+8 > len(data) {
				return out, fmt.Errorf("fixed64 overflow")
			}
			f.Fixed64 = binary.LittleEndian.Uint64(data[i : i+8])
			i += 8
		case WireBytes:
			l, n := ConsumeVarint(data[i:])
			if n == 0 || i+n+int(l) > len(data) {
				return out, fmt.Errorf("bad bytes")
			}
			i += n
			f.Bytes = append([]byte(nil), data[i:i+int(l)]...)
			i += int(l)
		case WireFixed32:
			if i+4 > len(data) {
				return out, fmt.Errorf("fixed32 overflow")
			}
			f.Fixed32 = binary.LittleEndian.Uint32(data[i : i+4])
			i += 4
		default:
			return out, fmt.Errorf("unsupported wire type %d field %d", wt, num)
		}
		out = append(out, f)
	}
	return out, nil
}

// ConsumeVarint decodes a varint; returns (0,0) on error.
func ConsumeVarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if c < 0x80 {
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, 0
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
