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
		// socks5h = remote DNS (x/net default); plain socks5 = local DNS then IP to proxy.
		if p.RDNS {
			sd, err := xproxy.SOCKS5("tcp", addr, auth, d.Direct)
			if err != nil {
				return nil, fmt.Errorf("socks5 dialer: %w", err)
			}
			d.inner = sd
		} else {
			d.inner = &socks5LocalDNSToIPDialer{proxyAddr: addr, auth: auth, forward: d.Direct}
		}
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

// --- SOCKS5 with local DNS (plain socks5:// semantics) ---

// socks5LocalDNSToIPDialer implements RFC 1928 "no-auth + user/pass" with the
// destination resolved locally and sent to the proxy as an IP, so resolution
// uses local DNS instead of the proxy's. Mirrors x/net/proxy SOCKS5 framing.
type socks5LocalDNSToIPDialer struct {
	proxyAddr string
	auth      *xproxy.Auth
	forward   *net.Dialer
}

func (s *socks5LocalDNSToIPDialer) Dial(network, address string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, address)
}

func (s *socks5LocalDNSToIPDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := net.LookupPort(network, portStr)
	if err != nil {
		return nil, err
	}
	// Resolve host locally, then send the proxy a raw IP.
	ips, err := s.forward.Resolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var ip net.IP
	if len(ips) > 0 {
		ip = net.ParseIP(ips[0])
	}
	if ip == nil {
		if p4, err4 := net.ResolveIPAddr("ip", host); err4 == nil {
			ip = p4.IP
		} else {
			return nil, err4
		}
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("socks5 local dns: no IPv4 address for %s", host)
	}

	conn, err := s.forward.DialContext(ctx, "tcp", s.proxyAddr)
	if err != nil {
		return nil, err
	}
	if err := writeSOCKS5Greeting(conn, s.auth); err != nil {
		conn.Close()
		return nil, err
	}
	// ATYP=1 IPv4.
	b := make([]byte, 0, 1+1+4+2)
	b = append(b, 0x05, 0x01, 0x00, ip4[0], ip4[1], ip4[2], ip4[3], byte(port>>8), byte(port))
	if _, err := conn.Write(b); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 4)
	if _, err := readFull(conn, resp); err != nil {
		conn.Close()
		return nil, err
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect rejected (status=%d)", resp[1])
	}
	return conn, nil
}

func writeSOCKS5Greeting(conn net.Conn, auth *xproxy.Auth) error {
	var b []byte
	if auth == nil || (auth.User == "" && auth.Password == "") {
		b = []byte{0x05, 0x01, 0x00}
	} else {
		b = []byte{0x05, 0x02, 0x00, 0x02}
	}
	if _, err := conn.Write(b); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := readFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 0x05 {
		return fmt.Errorf("socks5: bad version %d", resp[0])
	}
	switch resp[1] {
	case 0x00:
		return nil
	case 0x02:
		if auth == nil {
			return fmt.Errorf("socks5: proxy requires auth but none supplied")
		}
		if len(auth.User) > 255 || len(auth.Password) > 255 {
			return fmt.Errorf("socks5: auth user/pass too long")
		}
		b := make([]byte, 0, 3+len(auth.User)+len(auth.Password))
		b = append(b, 0x01, byte(len(auth.User)))
		b = append(b, auth.User...)
		b = append(b, byte(len(auth.Password)))
		b = append(b, auth.Password...)
		if _, err := conn.Write(b); err != nil {
			return err
		}
		if _, err := readFull(conn, resp); err != nil {
			return err
		}
		if resp[1] != 0x00 {
			return fmt.Errorf("socks5: auth failed (%d)", resp[1])
		}
		return nil
	default:
		return fmt.Errorf("socks5: no acceptable auth method (server chose %d)", resp[1])
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
