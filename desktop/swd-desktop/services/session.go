// Package services exposes the downloader's capabilities to the Wails
// frontend. Each service is a plain Go struct whose exported methods become
// callable from TypeScript via Wails bindings.
package services

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// SessionManager owns the anonymous Steam CM session shared by all services.
// It connects lazily, reconnects when the session drops, and serializes
// connect attempts. Proxy and stealth behaviour come from Settings.
type SessionManager struct {
	mu   sync.Mutex
	sess *steam.Session
	cdn  *cdn.Client
	log  *ui.Log

	proxyURL string
	outputDir string
}

// NewSessionManager builds a manager. proxyURL "" means direct (stealth TLS).
func NewSessionManager(proxyURL, outputDir string, log *ui.Log) *SessionManager {
	if log == nil {
		log, _ = ui.New(ui.Options{})
	}
	return &SessionManager{proxyURL: proxyURL, outputDir: outputDir, log: log}
}

// SetProxy updates the proxy and drops the current session so the next call
// reconnects with the new settings.
func (m *SessionManager) SetProxy(proxyURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyURL = proxyURL
	m.closeLocked()
}

// SetOutputDir updates the download directory.
func (m *SessionManager) SetOutputDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputDir = dir
}

// ProxyURL returns the current proxy ("" = direct).
func (m *SessionManager) ProxyURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proxyURL
}

// OutputDir returns the download directory.
func (m *SessionManager) OutputDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outputDir
}

// ensure returns a live session + CDN client, connecting if needed.
func (m *SessionManager) ensure(ctx context.Context) (*steam.Session, *cdn.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess != nil && m.sess.CM != nil && m.sess.CM.LoggedOn() {
		return m.sess, m.cdn, nil
	}
	m.closeLocked()
	sess, err := steam.InitSession(ctx, m.proxyURL, m.log)
	if err != nil {
		return nil, nil, err
	}
	m.sess = sess
	m.cdn = cdn.New(sess.CM, sess.HTTP(), m.log)
	return m.sess, m.cdn, nil
}

// CM returns the CM client of a live session (for workshop queries).
func (m *SessionManager) CM(ctx context.Context) (*steam.Session, error) {
	sess, _, err := m.ensure(ctx)
	return sess, err
}

// CDN returns the CDN client of a live session.
func (m *SessionManager) CDN(ctx context.Context) (*cdn.Client, error) {
	_, c, err := m.ensure(ctx)
	return c, err
}

// HTTP returns the session's HTTP client (proxy/stealth aware).
func (m *SessionManager) HTTP(ctx context.Context) (*http.Client, error) {
	sess, err := m.CM(ctx)
	if err != nil {
		return nil, err
	}
	return sess.HTTP(), nil
}

// Status reports connection health for the settings page.
func (m *SessionManager) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	loggedOn := m.sess != nil && m.sess.CM != nil && m.sess.CM.LoggedOn()
	return map[string]any{
		"connected":  loggedOn,
		"proxy":      m.proxyURL,
		"direct":     m.proxyURL == "",
		"output_dir": m.outputDir,
	}
}

// Close disconnects and releases the session.
func (m *SessionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

func (m *SessionManager) closeLocked() {
	if m.sess != nil {
		m.sess.Close()
		m.sess = nil
		m.cdn = nil
	}
}

// Warmup connects eagerly so the first UI action doesn't pay connect cost.
func (m *SessionManager) Warmup() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, _, err := m.ensure(ctx); err != nil && m.log != nil {
		m.log.Warn(fmt.Sprintf("steam warmup: %v", err))
	}
}
