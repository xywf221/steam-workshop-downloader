package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Settings is the persisted app configuration.
type Settings struct {
	ProxyURL  string `json:"proxy_url"`  // "" = direct (stealth TLS)
	OutputDir string `json:"output_dir"` // download directory
}

// SettingsService manages persisted settings and reports session status.
type SettingsService struct {
	sess *SessionManager
	subs *SubscriptionService

	mu   sync.Mutex
	path string
	cur  Settings
}

// NewSettingsService loads settings from dir and applies them to the session.
func NewSettingsService(sess *SessionManager, subs *SubscriptionService, configDir string) (*SettingsService, error) {
	s := &SettingsService{
		sess: sess,
		subs: subs,
		path: filepath.Join(configDir, "settings.json"),
	}
	_ = s.load()
	return s, nil
}

func (s *SettingsService) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		// Defaults from the session manager.
		s.cur = Settings{ProxyURL: s.sess.ProxyURL(), OutputDir: s.sess.OutputDir()}
		return nil
	}
	var st Settings
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	s.cur = st
	s.applyLocked()
	return nil
}

func (s *SettingsService) applyLocked() {
	s.sess.SetProxy(s.cur.ProxyURL)
	s.sess.SetOutputDir(s.cur.OutputDir)
	if s.subs != nil {
		s.subs.reload()
	}
}

// Get returns current settings.
func (s *SettingsService) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Set updates + persists settings and applies them live.
func (s *SettingsService) Set(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.cur = st
	s.applyLocked()
	return nil
}

// Status reports connection + config state for the settings page.
func (s *SettingsService) Status() (map[string]any, error) {
	return s.sess.Status(), nil
}

// TestConnection forces a (re)connect and reports the result.
func (s *SettingsService) TestConnection() (map[string]any, error) {
	s.sess.Warmup()
	st := s.sess.Status()
	if !st["connected"].(bool) {
		return st, fmt.Errorf("cannot connect to Steam (check proxy/network)")
	}
	return st, nil
}
