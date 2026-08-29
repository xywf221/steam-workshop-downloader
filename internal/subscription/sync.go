package subscription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/manifest"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// UpdateState describes one entry's freshness after a CheckUpdates pass.
type UpdateState struct {
	Entry      Entry
	RemoteGID  uint64 // current manifest gid on Steam (0 = lookup failed)
	HasUpdate  bool   // remote differs from local record
	NeverSync  bool   // subscribed but never downloaded
	CheckError string // non-empty when the remote lookup failed
}

// CheckUpdates queries Steam for every subscribed item and reports which
// have updates. Cheap: one batched GetDetails call, no file I/O.
func CheckUpdates(ctx context.Context, s *Store, client *cm.Client, log *ui.Log) ([]UpdateState, error) {
	entries := s.List()
	if len(entries) == 0 {
		return nil, nil
	}
	ids := make([]uint64, len(entries))
	for i, e := range entries {
		ids[i] = e.WorkshopID
	}
	items, err := workshop.GetDetails(ctx, client, ids, false, log)
	if err != nil {
		return nil, fmt.Errorf("GetDetails: %w", err)
	}
	byID := make(map[uint64]workshop.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	states := make([]UpdateState, 0, len(entries))
	for _, e := range entries {
		st := UpdateState{Entry: e, NeverSync: e.ManifestGID == 0}
		it, ok := byID[e.WorkshopID]
		if !ok || (it.Result != 0 && it.Result != cm.EResultOK) {
			st.CheckError = "item unavailable (deleted/private?)"
			states = append(states, st)
			continue
		}
		st.RemoteGID = it.HContentFile
		// Refresh title opportunistically.
		if it.Title != "" && it.Title != e.Title {
			_ = s.Add(Entry{WorkshopID: e.WorkshopID, AppID: it.ConsumerAppID, Title: it.Title})
		}
		st.HasUpdate = e.NeedsUpdate(it.HContentFile)
		states = append(states, st)
	}
	return states, nil
}

// SyncResult summarises one entry's incremental sync.
type SyncResult struct {
	WorkshopID uint64
	Title      string
	Skipped    bool // already up to date
	Stats      download.Stats
	Removed    []string // local files deleted because the manifest dropped them
	Err        error
}

// Sync downloads updates for the given entries (or all with pending updates
// when onlyUpdated is true). Incremental: DownloadItem skips files whose SHA
// already matches; files absent from the new manifest are deleted locally.
func Sync(
	ctx context.Context,
	s *Store,
	client *cm.Client,
	cdnClient *cdn.Client,
	outputDir string,
	ids []uint64, // empty = all entries needing update
	log *ui.Log,
	opts download.Options,
) []SyncResult {
	var targets []Entry
	if len(ids) > 0 {
		for _, id := range ids {
			if e, ok := s.Get(id); ok {
				targets = append(targets, e)
			}
		}
	} else {
		targets = s.List()
	}

	results := make([]SyncResult, 0, len(targets))
	for _, e := range targets {
		res := SyncResult{WorkshopID: e.WorkshopID, Title: e.Title}

		// Fetch manifest first so we know the gid + full file list.
		m, err := cdnClient.ManifestForWorkshopItem(ctx, e.WorkshopID)
		if err != nil {
			res.Err = fmt.Errorf("manifest: %w", err)
			results = append(results, res)
			continue
		}
		if m.Name != "" {
			res.Title = m.Name
		}
		if !e.NeedsUpdate(m.GID) && e.ManifestGID != 0 {
			res.Skipped = true
			results = append(results, res)
			continue
		}

		// Download (DownloadItem skips SHA-matching files automatically).
		stats := download.DownloadItem(ctx, cdnClient, e.WorkshopID, outputDir, log, opts)
		res.Stats = stats
		if stats.FullyFailed() {
			res.Err = fmt.Errorf("download failed")
			results = append(results, res)
			continue
		}

		// Delete local files the new manifest no longer contains.
		res.Removed = pruneStaleFiles(e, m, outputDir, log)

		// Record the new state.
		files := manifestFiles(m)
		if err := s.MarkSynced(e.WorkshopID, m.GID, 0, files); err != nil && log != nil {
			log.Warn(fmt.Sprintf("record state for %d: %v", e.WorkshopID, err))
		}
		results = append(results, res)
	}
	return results
}

// manifestFiles returns the cleaned relative paths of all real files.
func manifestFiles(m *manifest.Manifest) []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		if f.IsDirectory() {
			continue
		}
		name := manifest.CleanName(f.Filename)
		if name == "" || name == "." {
			continue
		}
		out = append(out, filepath.FromSlash(name))
	}
	return out
}

// pruneStaleFiles deletes files recorded in the previous sync that the new
// manifest no longer lists. Returns removed relative paths.
func pruneStaleFiles(e Entry, m *manifest.Manifest, outputDir string, log *ui.Log) []string {
	if len(e.Files) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(m.Files))
	for _, f := range m.Files {
		name := manifest.CleanName(f.Filename)
		if name != "" && name != "." {
			want[filepath.FromSlash(name)] = struct{}{}
		}
	}
	itemDir := filepath.Join(outputDir, fmt.Sprintf("%d", e.WorkshopID))
	var removed []string
	for _, rel := range e.Files {
		if _, ok := want[rel]; ok {
			continue
		}
		// Guard: only delete inside the item dir, no traversal.
		clean := filepath.Clean(rel)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		p := filepath.Join(itemDir, clean)
		if err := os.Remove(p); err == nil {
			removed = append(removed, rel)
			if log != nil {
				log.Dim(fmt.Sprintf("  removed stale %s", rel))
			}
		}
	}
	return removed
}
