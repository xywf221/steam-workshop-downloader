package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// Dialer dials TCP through the configured proxy (or direct if Parsed is zero / nil setup).
type Dialer struct {
	parsed *Parsed
	inner  xproxy.Dialer
	// Direct is used when no proxy is configured.
	Direct *net.Dialer
}

// NewDialer builds a Dialer from a parsed proxy. If p is nil, dials direct.
func NewDialer(p *Parsed) (*Dialer, error) {
	d := &Dialer{
		parsed: p,
		Direct: &net.Dialer{Timeout: 30 * time.Second},
	}
	if p == nil {
		return d, nil
	}

	addr := net.JoinHostPort(p.Host, fmt.Sprintf("%d", p.Port))
	var auth *xproxy.Auth
	if p.Username != "" || p.Password != "" || p.HasAuth {
		auth = &xproxy.Auth{User: p.Username, Password: p.Password}
	}

	switch p.Proto {
	case ProtoSOCKS5:
		// golang.org/x/net/proxy SOCKS5 always does remote DNS when host is a name.
		sd, err := xproxy.SOCKS5("tcp", addr, auth, d.Direct)
		if err != nil {
			return nil, fmt.Errorf("socks5 dialer: %w", err)
		}
		d.inner = sd
	case ProtoSOCKS4:
		// x/net/proxy has no first-class SOCKS4; use SOCKS5 dialer only for 5.
		// Implement minimal SOCKS4 via custom dialer.
		d.inner = &socks4Dialer{proxyAddr: addr, userID: p.Username, forward: d.Direct}
	case ProtoHTTP:
		d.inner = &httpConnectDialer{
			proxyURL: &url.URL{
				Scheme: "http",
				Host:   addr,
				User:   urlUser(p),
			},
			forward: d.Direct,
		}
	default:
		return nil, fmt.Errorf("unsupported proxy proto %s", p.Proto)
	}
	return d, nil
}

func urlUser(p *Parsed) *url.Userinfo {
	if p.Username == "" && p.Password == "" && !p.HasAuth {
		return nil
	}
	if p.Password != "" || (p.HasAuth && p.Username != "") {
		return url.UserPassword(p.Username, p.Password)
	}
	return url.User(p.Username)
}

// DialContext dials network/address, optionally through the proxy.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("proxy dialer only supports tcp, got %s", network)
	}
	if d.inner == nil {
		return d.Direct.DialContext(ctx, network, address)
	}
	// x/net dialers are not all Context-aware; honour deadline from ctx.
	if deadline, ok := ctx.Deadline(); ok {
		type dialerDeadline interface {
			SetDeadline(time.Time) error
		}
		_ = deadline
	}
	type contextDialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	if cd, ok := d.inner.(contextDialer); ok {
		return cd.DialContext(ctx, network, address)
	}
	// Fallback: dial in a goroutine so we can cancel.
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := d.inner.Dial(network, address)
		ch <- result{c, err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		return r.c, r.err
	}
}

// --- SOCKS4 (minimal CONNECT) ---

type socks4Dialer struct {
	proxyAddr string
	userID    string
	forward   *net.Dialer
}

func (s *socks4Dialer) Dial(network, address string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, address)
}

func (s *socks4Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// Resolve host locally (SOCKS4 has no domain form unless 4a; we use 4a).
	ip := net.ParseIP(host)
	use4a := ip == nil
	if ip != nil {
		ip = ip.To4()
		if ip == nil {
			return nil, fmt.Errorf("socks4: IPv6 not supported")
		}
	}

	conn, err := s.forward.DialContext(ctx, "tcp", s.proxyAddr)
	if err != nil {
		return nil, err
	}

	// VN=4, CD=1 CONNECT
	req := []byte{0x04, 0x01, byte(port >> 8), byte(port)}
	if use4a {
		// IP 0.0.0.x with x!=0 signals SOCKS4a; domain follows userID NUL.
		req = append(req, 0, 0, 0, 1)
		req = append(req, []byte(s.userID)...)
		req = append(req, 0)
		req = append(req, []byte(host)...)
		req = append(req, 0)
	} else {
		req = append(req, ip[0], ip[1], ip[2], ip[3])
		req = append(req, []byte(s.userID)...)
		req = append(req, 0)
	}
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 8)
	if _, err := readFull(conn, resp); err != nil {
		conn.Close()
		return nil, err
	}
	if resp[0] != 0x00 || resp[1] != 0x5a {
		conn.Close()
		return nil, fmt.Errorf("socks4 connect rejected (status=0x%02x)", resp[1])
	}
	return conn, nil
}

// --- HTTP CONNECT ---

type httpConnectDialer struct {
	proxyURL *url.URL
	forward  *net.Dialer
}

func (h *httpConnectDialer) Dial(network, address string) (net.Conn, error) {
	return h.DialContext(context.Background(), network, address)
}

func (h *httpConnectDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := h.forward.DialContext(ctx, "tcp", h.proxyURL.Host)
	if err != nil {
		return nil, err
	}
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)
	if h.proxyURL.User != nil {
		user := h.proxyURL.User.Username()
		pass, _ := h.proxyURL.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		req += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	// Read until end of HTTP headers.
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	for {
		if _, err := conn.Read(tmp); err != nil {
			conn.Close()
			return nil, err
		}
		buf = append(buf, tmp[0])
		if len(buf) >= 4 && string(buf[len(buf)-4:]) == "\r\n\r\n" {
			break
		}
		if len(buf) > 8192 {
			conn.Close()
			return nil, fmt.Errorf("http proxy: response headers too large")
		}
	}
	// Status line: HTTP/1.x 200 ...
	line := string(buf)
	if len(line) < 12 || !(line[9] == '2' && line[10] == '0' && line[11] == '0') {
		conn.Close()
		// trim first line for error
		end := len(line)
		for i := 0; i < len(line); i++ {
			if line[i] == '\r' {
				end = i
				break
			}
		}
		return nil, fmt.Errorf("http proxy CONNECT failed: %s", line[:end])
	}
	return conn, nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
