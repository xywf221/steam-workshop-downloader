package web

import "testing"

func TestIsAllowedMediaHost(t *testing.T) {
	ok := []string{
		"steamuserimages-a.akamaihd.net",
		"shared.cloudflare.steamstatic.com",
		"steamcommunity.com",
		"cdn.akamai.steamstatic.com",
	}
	for _, h := range ok {
		if !IsAllowedMediaHost(h) {
			t.Errorf("expected allowed: %s", h)
		}
	}
	bad := []string{"evil.com", "127.0.0.1", "192.168.1.1", "localhost", ""}
	for _, h := range bad {
		if IsAllowedMediaHost(h) {
			t.Errorf("expected denied: %s", h)
		}
	}
}

func TestValidateMediaURL(t *testing.T) {
	u, err := ValidateMediaURL("https://steamcommunity.com/foo.png")
	if err != nil || u == "" {
		t.Fatalf("valid url: %v %q", err, u)
	}
	if _, err := ValidateMediaURL("https://evil.com/x"); err == nil {
		t.Fatal("expected reject evil host")
	}
	if _, err := ValidateMediaURL("file:///etc/passwd"); err == nil {
		t.Fatal("expected reject file scheme")
	}
	if _, err := ValidateMediaURL("https://user:pass@steamcommunity.com/x"); err == nil {
		t.Fatal("expected reject userinfo")
	}
}

func TestDisplayPreviewURL(t *testing.T) {
	orig := "https://steamcommunity.com/img.png"
	if got := DisplayPreviewURL(orig, ""); got != orig {
		t.Fatalf("no proxy: got %q", got)
	}
	got := DisplayPreviewURL(orig, "socks5://127.0.0.1:1080")
	if got != MediaProxyPath(orig) {
		t.Fatalf("with proxy: got %q", got)
	}
}
