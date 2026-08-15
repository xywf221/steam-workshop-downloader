// WebSocket (wss://) transport for Steam CM connections.
//
// Steam's CM websocket endpoints ([host]:443) carry the same Steam message
// stream as raw TCP, but each websocket binary message holds one Steam packet
// (raw emsg bytes — NO VT01 length/magic header, unlike TCP).
//
// This is a minimal RFC 6455 client speaker: opening handshake over TLS, then
// per-message binary frames. It matches how ValvePython / SteamKit2 talk to
// CM WebSocket endpoints without pulling in a websocket dependency.
package cm

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// wsFrame is a decoded WebSocket header.
type wsFrame struct {
	opcode byte
	length uint64
}

// wssConn adapts a TLS connection into a msgConn where each websocket binary
// message is one Steam packet.
type wssConn struct {
	rw  io.ReadWriteCloser // TLS conn
	br  *bufio.Reader
	raw net.Conn           // for deadlines
}

// newWSSConn performs the WebSocket opening handshake over c and returns a
// wssConn. host is the endpoint hostname (used as TLS SNI + Host header),
// port is the endpoint port.
func newWSSConn(c net.Conn, host string, port int) (*wssConn, error) {
	path := "/cmsocket/"
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)

	u := &url.URL{Scheme: "wss", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: path}
	req := &http.Request{
		Method:     "GET",
		URL:        u,
		Host:       u.Host,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Upgrade":              {"websocket"},
			"Connection":           {"Upgrade"},
			"Sec-WebSocket-Key":    {keyB64},
			"Sec-WebSocket-Version": {"13"},
		},
	}
	if err := req.Write(c); err != nil {
		return nil, err
	}

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("websocket handshake: HTTP %d", resp.StatusCode)
	}

	return &wssConn{rw: c, br: br, raw: c}, nil
}

// ReadMsg reads one complete websocket message (binary or text) and returns its
// payload. It returns io.EOF when the connection is closed cleanly.
func (w *wssConn) ReadMsg() ([]byte, error) {
	for {
		f, err := w.readFrame()
		if err != nil {
			return nil, err
		}
		switch f.opcode {
		case 0x2: // binary — a Steam message
			buf := make([]byte, int(f.length))
			if _, err := io.ReadFull(w.br, buf); err != nil {
				return nil, err
			}
			return buf, nil
		case 0x1: // text — discard (not used by Steam CM meaningfully)
			if _, err := io.CopyN(io.Discard, w.br, int64(f.length)); err != nil {
				return nil, err
			}
			continue
		case 0x8: // close
			if _, err := io.CopyN(io.Discard, w.br, int64(f.length)); err != nil {
				return nil, err
			}
			return nil, io.EOF
		case 0x9: // ping — reply pong
			payload := make([]byte, int(f.length))
			if _, err := io.ReadFull(w.br, payload); err != nil {
				return nil, err
			}
			if err := w.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA: // pong
			if _, err := io.CopyN(io.Discard, w.br, int64(f.length)); err != nil {
				return nil, err
			}
			continue
		default: // continuation / reserved
			if _, err := io.CopyN(io.Discard, w.br, int64(f.length)); err != nil {
				return nil, err
			}
			continue
		}
	}
}

// WriteMsg writes one binary message.
func (w *wssConn) WriteMsg(payload []byte) error {
	return w.writeFrame(0x2, payload)
}

func (w *wssConn) writeFrame(opcode byte, payload []byte) error {
	var hdr [2]byte
	hdr[0] = 0x80 | opcode
	n := len(payload)
	ext7 := uint64(n)
	switch {
	case n <= 125:
		hdr[1] = 0x80 | byte(n)
	case n <= 65535:
		hdr[1] = 0x80 | 126
	default:
		hdr[1] = 0x80 | 127
	}
	if _, err := w.rw.Write(hdr[:]); err != nil {
		return err
	}
	switch {
	case n <= 125:
		// no extended length
	case n <= 65535:
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := w.rw.Write(ext[:]); err != nil {
			return err
		}
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], ext7)
		if _, err := w.rw.Write(ext[:]); err != nil {
			return err
		}
	}
	// Client frames must be masked.
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	if _, err := w.rw.Write(mask[:]); err != nil {
		return err
	}
	masked := make([]byte, n, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i&3]
	}
	if _, err := w.rw.Write(masked); err != nil {
		return err
	}
	return nil
}

func (w *wssConn) readFrame() (wsFrame, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(w.br, hdr[:]); err != nil {
		return wsFrame{}, err
	}
	f := wsFrame{opcode: hdr[0] & 0x0f}
	// hdr[0]&0x80 is FIN; Steam never fragments a single CM packet across frames
	// but we must not rely on that, so ignore FIN.
	if hdr[1]&0x80 != 0 {
		return wsFrame{}, errors.New("masked server frame (unsupported)")
	}
	f.length = uint64(hdr[1] & 0x7f)
	switch f.length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return wsFrame{}, err
		}
		f.length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return wsFrame{}, err
		}
		f.length = binary.BigEndian.Uint64(ext[:])
	}
	return f, nil
}

func (w *wssConn) SetReadDeadline(t time.Time) error {
	if w.raw != nil {
		return w.raw.SetReadDeadline(t)
	}
	return nil
}

func (w *wssConn) SetWriteDeadline(t time.Time) error {
	if w.raw != nil {
		return w.raw.SetWriteDeadline(t)
	}
	return nil
}

func (w *wssConn) Close() error {
	_ = w.writeFrame(0x8, nil)
	return w.rw.Close()
}