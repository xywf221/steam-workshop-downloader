package subscription

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/manifest"
)

func TestPruneStaleFiles(t *testing.T) {
	dir := t.TempDir()
	itemDir := filepath.Join(dir, "42")
	if err := os.MkdirAll(filepath.Join(itemDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(itemDir, "keep.txt")
	stale := filepath.Join(itemDir, "sub", "stale.txt")
	for _, p := range []string{keep, stale} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	e := Entry{
		WorkshopID: 42,
		Files:      []string{"keep.txt", filepath.Join("sub", "stale.txt")},
	}
	m := &manifest.Manifest{
		Files: []manifest.File{
			{Filename: "keep.txt"},
		},
	}
	removed := pruneStaleFiles(e, m, dir, nil)
	if len(removed) != 1 {
		t.Fatalf("expected 1 removal, got %v", removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("keep.txt should survive")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale.txt should be deleted")
	}
}

func TestPruneStaleFilesTraversalGuard(t *testing.T) {
	dir := t.TempDir()
	e := Entry{WorkshopID: 42, Files: []string{"..\\evil.txt", "../evil2.txt", "sub/../../evil3.txt"}}
	m := &manifest.Manifest{}
	if got := pruneStaleFiles(e, m, dir, nil); len(got) != 0 {
		t.Fatalf("traversal entries must be skipped, removed %v", got)
	}
}

func TestManifestFiles(t *testing.T) {
	m := &manifest.Manifest{
		Files: []manifest.File{
			{Filename: "a/b.txt"},
			{Filename: "dir", Flags: 0x40}, // directory — excluded
			{Filename: "c.txt"},
		},
	}
	files := manifestFiles(m)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %v", files)
	}
}
