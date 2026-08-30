package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/subscription"
)

// Subscription is one followed item with its update state.
type Subscription struct {
	WorkshopID uint64 `json:"workshop_id"`
	AppID      uint32 `json:"app_id"`
	Title      string `json:"title"`
	Manifest   uint64 `json:"manifest"`
	RemoteGID  uint64 `json:"remote_gid"`
	HasUpdate  bool   `json:"has_update"`
	NeverSync  bool   `json:"never_sync"`
	Error      string `json:"error,omitempty"`
	Synced     string `json:"synced,omitempty"`
}

// SyncResult reports one item's incremental update.
type SyncResult struct {
	WorkshopID uint64   `json:"workshop_id"`
	Title      string   `json:"title"`
	Skipped    bool     `json:"skipped"`
	FilesOK    int      `json:"files_ok"`
	Removed    []string `json:"removed"`
	Error      string   `json:"error,omitempty"`
}

// SubscriptionService manages the followed-items list and incremental updates.
type SubscriptionService struct {
	sess *SessionManager
	dl   *DownloadService

	mu    sync.Mutex
	store *subscription.Store
}

// NewSubscriptionService builds the service.
func NewSubscriptionService(sess *SessionManager, dl *DownloadService) *SubscriptionService {
	return &SubscriptionService{sess: sess, dl: dl}
}

func (s *SubscriptionService) loadStore() (*subscription.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		return s.store, nil
	}
	dir := s.sess.OutputDir()
	if dir == "" {
		dir = "."
	}
	st, err := subscription.Load(dir)
	if err != nil {
		return nil, err
	}
	s.store = st
	return st, nil
}

// reload drops the cached store (e.g. after output dir changes).
func (s *SubscriptionService) reload() {
	s.mu.Lock()
	s.store = nil
	s.mu.Unlock()
}

// List returns subscriptions with live update state from Steam.
func (s *SubscriptionService) List() ([]Subscription, error) {
	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}
	if len(store.List()) == 0 {
		return []Subscription{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return nil, fmt.Errorf("steam session: %w", err)
	}
	states, err := subscription.CheckUpdates(ctx, store, sess.CM, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(states))
	for _, st := range states {
		synced := ""
		if st.Entry.UpdatedAt != nil {
			synced = st.Entry.UpdatedAt.Format("2006-01-02 15:04")
		}
		out = append(out, Subscription{
			WorkshopID: st.Entry.WorkshopID,
			AppID:      st.Entry.AppID,
			Title:      st.Entry.Title,
			Manifest:   st.Entry.ManifestGID,
			RemoteGID:  st.RemoteGID,
			HasUpdate:  st.HasUpdate || st.NeverSync,
			NeverSync:  st.NeverSync,
			Error:      st.CheckError,
			Synced:     synced,
		})
	}
	return out, nil
}

// Add subscribes to one item (collections expand into children).
func (s *SubscriptionService) Add(appID int, workshopID uint64) (int, error) {
	if appID <= 0 || workshopID == 0 {
		return 0, fmt.Errorf("appid and workshopid required")
	}
	store, err := s.loadStore()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return 0, fmt.Errorf("steam session: %w", err)
	}
	ids, err := workshop.ResolveIDs(ctx, sess.CM, []uint64{workshopID}, nil)
	if err != nil {
		return 0, err
	}
	items, _ := workshop.GetDetails(ctx, sess.CM, ids, false, nil)
	titles := make(map[uint64]string, len(items))
	for _, it := range items {
		titles[it.ID] = it.Title
	}
	added := 0
	for _, id := range ids {
		t := titles[id]
		if t == "" {
			t = fmt.Sprintf("%d", id)
		}
		if err := store.Add(subscription.Entry{WorkshopID: id, AppID: uint32(appID), Title: t}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// Remove unsubscribes one item (downloaded files are kept).
func (s *SubscriptionService) Remove(workshopID uint64) error {
	store, err := s.loadStore()
	if err != nil {
		return err
	}
	return store.Remove(workshopID)
}

// Update incrementally syncs subscriptions (one id, or all when id == 0).
func (s *SubscriptionService) Update(workshopID uint64) ([]SyncResult, error) {
	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}
	var ids []uint64
	if workshopID != 0 {
		ids = []uint64{workshopID}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return nil, fmt.Errorf("steam session: %w", err)
	}
	cdnClient, err := s.sess.CDN(ctx)
	if err != nil {
		return nil, err
	}
	results := subscription.Sync(ctx, store, sess.CM, cdnClient, s.sess.OutputDir(), ids, nil, download.Options{Retries: 5})
	out := make([]SyncResult, 0, len(results))
	for _, r := range results {
		sr := SyncResult{
			WorkshopID: r.WorkshopID,
			Title:      r.Title,
			Skipped:    r.Skipped,
			Removed:    r.Removed,
		}
		if r.Err != nil {
			sr.Error = r.Err.Error()
		} else {
			sr.FilesOK = r.Stats.OK
		}
		out = append(out, sr)
	}
	return out, nil
}
