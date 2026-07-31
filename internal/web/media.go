package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxMediaBytes      = 8 * 1024 * 1024
	mediaTimeout       = 12 * time.Second
	maxMediaConcurrent = 3
)

var mediaSem = make(chan struct{}, maxMediaConcurrent)

// Host suffixes Valve uses for Workshop previews / community images.
var allowedHostSuffixes = []string{
	"steamstatic.com",
	"steamusercontent.com",
	"akamaihd.net",
	"steamcommunity.com",
	"steampowered.com",
	"steamserver.net",
}

// IsAllowedMediaHost returns true if host is a known Steam CDN / community host.
func IsAllowedMediaHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(strings.TrimRight(host, ".")))
	if h == "" || h == "localhost" || strings.HasSuffix(h, ".local") {
		return false
	}
	// Reject raw IPs (SSRF).
	if ip := net.ParseIP(h); ip != nil {
		return false
	}
	for _, suf := range allowedHostSuffixes {
		if h == suf || strings.HasSuffix(h, "."+suf) {
			return true
		}
	}
	return false
}

// ValidateMediaURL normalizes and validates a media URL.
func ValidateMediaURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url scheme must be http or https")
	}
	if u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid host")
	}
	host := u.Hostname()
	if !IsAllowedMediaHost(host) {
		return "", fmt.Errorf("host not allowed: %s", host)
	}
	u.Fragment = ""
	return u.String(), nil
}

// FetchMedia downloads a Steam preview image, optionally via proxy.
// httpClient should already honour --proxy when set (from steam session).
func FetchMedia(httpClient *http.Client, rawURL string) (body []byte, contentType string, err error) {
	cleaned, err := ValidateMediaURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	mediaSem <- struct{}{}
	defer func() { <-mediaSem }()

	req, err := http.NewRequest(http.MethodGet, cleaned, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "swd-web-media/1.0")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	client := *httpClient
	client.Timeout = mediaTimeout

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	ctype := resp.Header.Get("Content-Type")
	if i := strings.Index(ctype, ";"); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	allowed := strings.HasPrefix(ctype, "image/") ||
		ctype == "application/octet-stream" ||
		ctype == "binary/octet-stream"
	if !allowed {
		return nil, "", fmt.Errorf("unexpected content-type: %s", ctype)
	}
	limited := io.LimitReader(resp.Body, maxMediaBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	if len(data) > maxMediaBytes {
		return nil, "", fmt.Errorf("media exceeds %d bytes", maxMediaBytes)
	}
	return data, ctype, nil
}

// MediaProxyPath is the relative path for the media route.
func MediaProxyPath(originalURL string) string {
	return "/media/image?u=" + url.QueryEscape(originalURL)
}

// DisplayPreviewURL returns what HTML <img src> should use.
// When proxyURL is set, rewrite to local media proxy so the browser never hits Steam CDNs directly.
func DisplayPreviewURL(originalURL, proxyURL string) string {
	if originalURL == "" {
		return ""
	}
	if proxyURL == "" {
		return originalURL
	}
	if _, err := ValidateMediaURL(originalURL); err != nil {
		return originalURL
	}
	return MediaProxyPath(originalURL)
}

