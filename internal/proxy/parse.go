// Package proxy parses proxy URLs and builds dialers for CM / CDN / media.
package proxy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Proto is the transport protocol for a proxy.
type Proto string

const (
	ProtoSOCKS5 Proto = "SOCKS5"
	ProtoSOCKS4 Proto = "SOCKS4"
	ProtoHTTP   Proto = "HTTP"
)

var schemeRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.\-]*)://(.*)$`)

var protocolByScheme = map[string]Proto{
	"socks5":  ProtoSOCKS5,
	"socks5h": ProtoSOCKS5,
	"socks4":  ProtoSOCKS4,
	"http":    ProtoHTTP,
	"https":   ProtoHTTP,
}

// Parsed is a proxy URL ready to hand to a dialer.
type Parsed struct {
	Proto    Proto
	Host     string
	Port     int
	Username string // empty if none
	Password string // empty if none; may be set with empty meaning "present but blank"
	HasAuth  bool   // true if userinfo was present
	RDNS     bool
	Scheme   string
	Original string
}

// RedactedURL returns a log-safe URL with the password replaced by ***.
func (p Parsed) RedactedURL() string {
	auth := ""
	if p.HasAuth || p.Username != "" {
		auth = p.Username
		if p.Password != "" || p.HasAuth {
			// Always show :*** when password was provided (including empty).
			if strings.Contains(p.Original, "@") {
				// If original had user:pass@ or user@, mirror form.
				if p.Password != "" || strings.Contains(userinfoOf(p.Original), ":") {
					auth += ":***"
				}
			}
		}
		auth += "@"
	}
	scheme := p.Scheme
	if scheme == "" {
		scheme = "socks5"
	}
	return fmt.Sprintf("%s://%s%s:%d", scheme, auth, p.Host, p.Port)
}

func userinfoOf(url string) string {
	// crude: after :// before last @
	i := strings.Index(url, "://")
	rest := url
	if i >= 0 {
		rest = url[i+3:]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		return rest[:at]
	}
	return ""
}

// Parse parses a proxy URL into Parsed.
//
// Supported schemes (case-insensitive): socks5://, socks5h://, socks4://,
// http://, https://. Bare host:port defaults to socks5.
// user:password@ is accepted for all schemes.
func Parse(url string) (Parsed, error) {
	raw := strings.TrimSpace(url)
	if raw == "" {
		return Parsed{}, fmt.Errorf("empty proxy URL")
	}

	scheme := "socks5"
	rest := raw
	if m := schemeRE.FindStringSubmatch(raw); m != nil {
		scheme = strings.ToLower(m[1])
		rest = m[2]
	}

	proto, ok := protocolByScheme[scheme]
	if !ok {
		return Parsed{}, fmt.Errorf(
			"unsupported proxy scheme %q in %q (expected socks5://, socks4://, or http(s)://)",
			scheme, url,
		)
	}
	// socks5h:// (or socks5 with "h") = proxy-side DNS; plain socks5:// = local DNS.
	rdns := scheme == "socks5h" || scheme == "socks5hd"

	var user, password string
	hasAuth := false
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		hasAuth = true
		userinfo := rest[:at]
		rest = rest[at+1:]
		if colon := strings.Index(userinfo, ":"); colon >= 0 {
			user = userinfo[:colon]
			password = userinfo[colon+1:]
		} else {
			user = userinfo
		}
	}

	host, portStr, ok := splitHostPort(rest)
	if !ok {
		return Parsed{}, fmt.Errorf(
			"invalid proxy URL %q: expected host:port (e.g. socks5://127.0.0.1:1080)",
			url,
		)
	}
	if host == "" {
		return Parsed{}, fmt.Errorf("invalid proxy URL %q: empty host", url)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Parsed{}, fmt.Errorf("invalid proxy port in %q: %q", url, portStr)
	}

	return Parsed{
		Proto:    proto,
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
		HasAuth:  hasAuth,
		RDNS:     rdns,
		Scheme:   scheme,
		Original: url,
	}, nil
}

// splitHostPort splits host:port; host may be IPv6 in [brackets] (not fully
// validated here — workshop proxies are almost always IPv4).
func splitHostPort(rest string) (host, port string, ok bool) {
	// rsplit on :
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}
