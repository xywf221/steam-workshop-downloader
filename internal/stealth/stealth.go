// Package stealth provides direct-connection TLS without an SNI extension.
//
// Some networks (notably mainland China) reset TLS connections whose
// ClientHello carries an SNI for Steam domains (store.steampowered.com,
// api.steampowered.com, ...). The same servers happily answer when the
// handshake omits SNI entirely — Akamai/Valve edges fall back to a default
// certificate that is still valid for the intended host. We then verify the
// certificate manually against the original hostname, so TLS authenticity is
// preserved; only the (plaintext, censor-visible) SNI field is dropped.
//
// Usage: build an *http.Client via HTTPClient(), or use DialTLS for the CM
// websocket transport. Both keep the HTTP Host header / WS handshake host
// untouched — only the TLS ClientHello changes.
package stealth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// dialer is the shared TCP dialer for stealth connections.
var dialer = &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}

// noSNIHosts lists hosts whose edges answer a no-SNI handshake with a
// certificate valid for the host. Hosts NOT in this list keep normal SNI —
// some edges (notably api.steampowered.com) actively reject no-SNI
// handshakes, and api is reachable directly anyway.
var noSNIHosts = map[string]bool{
	"store.steampowered.com":        true,
	"steamcommunity.com":            true,
	"login.steampowered.com":        true,
	"checkout.steampowered.com":     true,
	"help.steampowered.com":         true,
	"steamcommunity-a.akamaihd.net": true,
}

// NeedsNoSNI reports whether host should be handshaken without SNI.
func NeedsNoSNI(host string) bool {
	return noSNIHosts[host]
}

// DialTLS dials host:port over TCP and completes a TLS handshake WITHOUT the
// SNI extension. The returned connection's certificate has been verified
// against serverName (chain + SAN), so callers may trust it exactly like a
// normal tls.Conn.
func DialTLS(ctx context.Context, serverName string, port int) (*tls.Conn, error) {
	addr := net.JoinHostPort(serverName, fmt.Sprintf("%d", port))
	return DialTLSAddr(ctx, addr, serverName)
}

// DialTLSAddr is DialTLS with an explicit dial address (IP literal allowed).
// serverName is used ONLY for certificate verification, never sent on wire.
func DialTLSAddr(ctx context.Context, addr, serverName string) (*tls.Conn, error) {
	tcp, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("stealth dial %s: %w", addr, err)
	}
	conn, err := HandshakeTLS(ctx, tcp, serverName)
	if err != nil {
		_ = tcp.Close()
		return nil, err
	}
	return conn, nil
}

// HandshakeTLS performs a no-SNI TLS handshake over an existing connection
// and verifies the peer certificate against serverName.
func HandshakeTLS(ctx context.Context, raw net.Conn, serverName string) (*tls.Conn, error) {
	// ServerName intentionally empty: Go omits the SNI extension.
	// Verification is done manually below (InsecureSkipVerify only skips the
	// automatic check because there is no name to check against yet).
	c := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true, // verified manually in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyPeer(cs, serverName)
		},
		MinVersion: tls.VersionTLS12,
	})
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("stealth tls %s: %w", serverName, err)
	}
	return c, nil
}

// verifyPeer checks the peer certificate chain and hostname as crypto/tls
// would have, using the system root pool (cached — SystemCertPool is
// expensive on Windows).
func verifyPeer(cs tls.ConnectionState, serverName string) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("stealth tls %s: no peer certificate", serverName)
	}
	roots := systemRoots()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	opts := x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         roots,
		Intermediates: x509.NewCertPool(),
	}
	for _, ic := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(ic)
	}
	if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
		return fmt.Errorf("stealth tls verify %s: %w", serverName, err)
	}
	return nil
}

// HTTPClient returns an *http.Client whose HTTPS requests omit SNI but keep
// full certificate verification. Plain HTTP requests pass through unchanged.
// Safe for concurrent use; connections are pooled per host.
func HTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: Transport(),
	}
}

// Transport returns an *http.Transport that omits SNI only for hosts known
// to be SNI-blocked; everything else uses normal TLS.
func Transport() *http.Transport {
	return &http.Transport{
		Proxy: nil, // stealth mode is a no-proxy feature by definition
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if NeedsNoSNI(host) {
				return DialTLSAddr(ctx, addr, host)
			}
			// Normal TLS with SNI.
			tcp, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			c := tls.Client(tcp, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			if err := c.HandshakeContext(ctx); err != nil {
				_ = tcp.Close()
				return nil, err
			}
			return c, nil
		},
		ForceAttemptHTTP2:     false, // ALPN is off without SNI on some edges; keep h1
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
	}
}

// cached verify pools: SystemCertPool is expensive on Windows.
var (
	rootsOnce sync.Once
	rootsPool *x509.CertPool
)

func systemRoots() *x509.CertPool {
	rootsOnce.Do(func() {
		p, err := x509.SystemCertPool()
		if err == nil && p != nil {
			rootsPool = p
		}
	})
	return rootsPool
}
