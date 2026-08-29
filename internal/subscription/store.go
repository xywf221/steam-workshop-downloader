// Package subscription maintains a local list of workshop items the user
// follows, records each item's last-downloaded manifest, and applies
// incremental updates (new/changed files downloaded, removed files deleted).
//
// Storage is a single JSON file (subscriptions.json) inside the output dir,
// shared by the CLI and the web UI.
package subscription

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileName is the store file inside the output directory.
const FileName = "subscriptions.json"

// Entry is one subscribed workshop item.
type Entry struct {
	WorkshopID uint64 `json:"workshop_id"`
	AppID      uint32 `json:"app_id"`
	Title      string `json:"title"`

	// ManifestGID is the last-downloaded depot manifest id (HContentFile).
	// Zero means never successfully downloaded.
	ManifestGID uint64 `json:"manifest_gid,omitempty"`
	// TimeUpdated is the item's time_updated at last sync (display only).
	TimeUpdated uint32 `json:"time_updated,omitempty"`

	// Files lists relative paths present after the last successful sync.
	// Used to delete files the new manifest no longer contains.
	Files []string `json:"files,omitempty"`

	AddedAt   time.Time  `json:"added_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"` // last successful sync
}

// NeedsUpdate reports whether a freshly fetched manifest gid differs from
// what we have on disk.
func (e *Entry) NeedsUpdate(remoteGID uint64) bool {
	return remoteGID != 0 && remoteGID != e.ManifestGID
}

// Store is a thread-safe, JSON-backed subscription list.
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[uint64]*Entry // keyed by WorkshopID
}

// Load reads the store from dir (missing file ⇒ empty store).
func Load(dir string) (*Store, error) {
	s := &Store{
		path:    filepath.Join(dir, FileName),
		entries: make(map[uint64]*Entry),
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var list []Entry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for i := range list {
		e := list[i]
		s.entries[e.WorkshopID] = &e
	}
	return s, nil
}

// saveLocked persists; caller holds mu.
func (s *Store) saveLocked() error {
	list := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, *e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].WorkshopID < list[j].WorkshopID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns all entries sorted by workshop id.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkshopID < out[j].WorkshopID })
	return out
}

// Get returns one entry (copy), or false.
func (s *Store) Get(id uint64) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Add inserts or refreshes a subscription (title/app updated if already present).
func (s *Store) Add(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[e.WorkshopID]; ok {
		// Preserve download state; refresh metadata.
		old.Title = e.Title
		old.AppID = e.AppID
	} else {
		e.AddedAt = time.Now()
		s.entries[e.WorkshopID] = &e
	}
	return s.saveLocked()
}

// Remove deletes a subscription (does NOT touch downloaded files).
func (s *Store) Remove(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	return s.saveLocked()
}

// MarkSynced records a successful sync: manifest gid, file list, timestamps.
func (s *Store) MarkSynced(id uint64, gid uint64, timeUpdated uint32, files []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return fmt.Errorf("subscription %d not found", id)
	}
	e.ManifestGID = gid
	e.TimeUpdated = timeUpdated
	e.Files = files
	now := time.Now()
	e.UpdatedAt = &now
	return s.saveLocked()
}
