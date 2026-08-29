package subscription

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAddListRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty store")
	}

	if err := s.Add(Entry{WorkshopID: 111, AppID: 294100, Title: "Mod A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Entry{WorkshopID: 222, AppID: 294100, Title: "Mod B"}); err != nil {
		t.Fatal(err)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}

	// Re-add refreshes metadata but keeps state.
	if err := s.MarkSynced(111, 999, 0, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Entry{WorkshopID: 111, AppID: 294100, Title: "Mod A renamed"}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.Get(111)
	if e.Title != "Mod A renamed" {
		t.Fatalf("title not refreshed: %q", e.Title)
	}
	if e.ManifestGID != 999 {
		t.Fatalf("manifest gid lost on re-add: %d", e.ManifestGID)
	}

	// Persistence: reload from disk.
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	e2, ok := s2.Get(111)
	if !ok || e2.ManifestGID != 999 || len(e2.Files) != 1 {
		t.Fatalf("reloaded entry wrong: %+v ok=%v", e2, ok)
	}

	if err := s2.Remove(222); err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 1 {
		t.Fatal("remove failed")
	}
	// File on disk is valid JSON list.
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if len(data) == 0 || data[0] != '[' {
		t.Fatalf("store file malformed: %q", data[:min(len(data), 20)])
	}
}

func TestNeedsUpdate(t *testing.T) {
	e := &Entry{ManifestGID: 100}
	if !e.NeedsUpdate(101) {
		t.Fatal("gid change should signal update")
	}
	if e.NeedsUpdate(100) {
		t.Fatal("same gid should not signal update")
	}
	if e.NeedsUpdate(0) {
		t.Fatal("remote gid 0 (lookup failed) should not signal update")
	}
	fresh := &Entry{}
	if !fresh.NeedsUpdate(5) {
		t.Fatal("never-synced entry needs update")
	}
}

func TestLoadMissing(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil || s == nil {
		t.Fatalf("missing file should give empty store: %v", err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt file should error")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
