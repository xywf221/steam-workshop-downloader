package web

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxMediaBytes      = 8 * 1024 * 1024
	mediaTimeout       = 12 * time.Second
	maxMediaConcurrent = 4
	mediaCacheTTL      = 1 * time.Hour
	mediaCacheMax      = 64
	mediaCacheMaxBytes = 32 * 1024 * 1024
)

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

type mediaEntry struct {
	key         string
	body        []byte
	contentType string
	expires     time.Time
}

// MediaCache is a process-local LRU+TTL cache for preview bytes.
type MediaCache struct {
	mu       sync.Mutex
	maxItems int
	maxBytes int
	ttl      time.Duration
	bytes    int
	data     map[string]*list.Element
	order    *list.List
	sem      chan struct{}
}

// NewMediaCache builds a media cache with concurrency limit.
func NewMediaCache() *MediaCache {
	return &MediaCache{
		maxItems: mediaCacheMax,
		maxBytes: mediaCacheMaxBytes,
		ttl:      mediaCacheTTL,
		data:     make(map[string]*list.Element),
		order:    list.New(),
		sem:      make(chan struct{}, maxMediaConcurrent),
	}
}

func (c *MediaCache) get(key string) (body []byte, ctype string, ok bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, hit := c.data[key]
	if !hit {
		return nil, "", false
	}
	ent := el.Value.(*mediaEntry)
	if !ent.expires.After(now) {
		c.removeLocked(el)
		return nil, "", false
	}
	c.order.MoveToBack(el)
	return ent.body, ent.contentType, true
}

func (c *MediaCache) put(key string, body []byte, ctype string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, hit := c.data[key]; hit {
		old := el.Value.(*mediaEntry)
		c.bytes -= len(old.body)
		old.body = body
		old.contentType = ctype
		old.expires = time.Now().Add(c.ttl)
		c.bytes += len(body)
		c.order.MoveToBack(el)
		c.evictLocked()
		return
	}
	ent := &mediaEntry{
		key:         key,
		body:        body,
		contentType: ctype,
		expires:     time.Now().Add(c.ttl),
	}
	el := c.order.PushBack(ent)
	c.data[key] = el
	c.bytes += len(body)
	c.evictLocked()
}

func (c *MediaCache) removeLocked(el *list.Element) {
	ent := el.Value.(*mediaEntry)
	c.order.Remove(el)
	delete(c.data, ent.key)
	c.bytes -= len(ent.body)
}

func (c *MediaCache) evictLocked() {
	for len(c.data) > c.maxItems || c.bytes > c.maxBytes {
		front := c.order.Front()
		if front == nil {
			break
		}
		c.removeLocked(front)
	}
}

// Fetch downloads (or returns cached) a Steam preview image.
// httpClient should already honour --proxy when set; it is never mutated.
func (c *MediaCache) Fetch(httpClient *http.Client, rawURL string) (body []byte, contentType string, err error) {
	cleaned, err := ValidateMediaURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	if body, ctype, ok := c.get(cleaned); ok {
		return body, ctype, nil
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	// Another fetcher may have filled the cache while we waited on the semaphore.
	if body, ctype, ok := c.get(cleaned); ok {
		return body, ctype, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), mediaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleaned, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "swd-web-media/1.0")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	resp, err := httpClient.Do(req)
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
	c.put(cleaned, data, ctype)
	return data, ctype, nil
}

// defaultMediaCache serves FetchMedia for tests and simple callers.
var defaultMediaCache = NewMediaCache()

// FetchMedia downloads a Steam preview (process-wide cache + concurrency limit).
func FetchMedia(httpClient *http.Client, rawURL string) (body []byte, contentType string, err error) {
	return defaultMediaCache.Fetch(httpClient, rawURL)
}

// MediaProxyPath is the relative path for the media route.
func MediaProxyPath(originalURL string) string {
	return "/media/image?u=" + url.QueryEscape(originalURL)
}

// DisplayPreviewURL returns what HTML <img src> should use.
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
