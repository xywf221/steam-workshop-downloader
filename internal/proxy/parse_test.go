package proxy

import "testing"

func TestParseValid(t *testing.T) {
	cases := []struct {
		url            string
		scheme, proto  string
		host           string
		port           int
		user, password string
		hasAuth        bool
	}{
		{"socks5://1.2.3.4:1080", "socks5", "SOCKS5", "1.2.3.4", 1080, "", "", false},
		{"socks5h://1.2.3.4:1080", "socks5h", "SOCKS5", "1.2.3.4", 1080, "", "", false},
		{"socks4://1.2.3.4:1080", "socks4", "SOCKS4", "1.2.3.4", 1080, "", "", false},
		{"http://1.2.3.4:8080", "http", "HTTP", "1.2.3.4", 8080, "", "", false},
		{"https://1.2.3.4:8080", "https", "HTTP", "1.2.3.4", 8080, "", "", false},
		{"http://alice:secret@proxy.example.com:8080", "http", "HTTP", "proxy.example.com", 8080, "alice", "secret", true},
		{"socks5://jp:a5472118@192.168.1.5:2260", "socks5", "SOCKS5", "192.168.1.5", 2260, "jp", "a5472118", true},
		{"socks5h://bob:p%40ss@proxy.example:1080", "socks5h", "SOCKS5", "proxy.example", 1080, "bob", "p%40ss", true},
		{"http://onlyuser@proxy:8080", "http", "HTTP", "proxy", 8080, "onlyuser", "", true},
		{"5.6.7.8:9100", "socks5", "SOCKS5", "5.6.7.8", 9100, "", "", false},
		{"HTTP://proxy:8080", "http", "HTTP", "proxy", 8080, "", "", false},
	}
	for _, c := range cases {
		p, err := Parse(c.url)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.url, err)
		}
		if p.Scheme != c.scheme || string(p.Proto) != c.proto || p.Host != c.host || p.Port != c.port {
			t.Errorf("Parse(%q) got scheme=%s proto=%s host=%s port=%d", c.url, p.Scheme, p.Proto, p.Host, p.Port)
		}
		if p.Username != c.user || p.Password != c.password || p.HasAuth != c.hasAuth {
			t.Errorf("Parse(%q) auth user=%q pass=%q has=%v", c.url, p.Username, p.Password, p.HasAuth)
		}
		if !p.RDNS {
			t.Errorf("Parse(%q) RDNS want true", c.url)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, url := range []string{
		"gopher://x:y/z",
		"ftp://1.2.3.4:21",
		"socks5://no-port",
		"socks5://1.2.3.4:abc",
		"",
	} {
		if _, err := Parse(url); err == nil {
			t.Errorf("Parse(%q) expected error", url)
		}
	}
}

func TestRedactedURL(t *testing.T) {
	p, err := Parse("socks5://jp:a5472118@192.168.1.5:2260")
	if err != nil {
		t.Fatal(err)
	}
	got := p.RedactedURL()
	if got != "socks5://jp:***@192.168.1.5:2260" {
		t.Errorf("RedactedURL=%q", got)
	}
	if contains(got, "a5472118") {
		t.Error("password leaked in redacted URL")
	}
	bare, _ := Parse("socks5://1.2.3.4:1080")
	if bare.RedactedURL() != "socks5://1.2.3.4:1080" {
		t.Errorf("bare redacted=%q", bare.RedactedURL())
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
