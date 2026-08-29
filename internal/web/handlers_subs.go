package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/subscription"
)

// subscriptionsPage renders the subscription management UI.
func (s *Server) subscriptionsPage(w http.ResponseWriter, r *http.Request) {
	data := s.baseData()
	data["Subs"] = s.subStatesSnapshot()
	s.render(w, "subscriptions.html", data)
}

// subStatesSnapshot lists entries with their last-known update state.
// It does NOT hit Steam — the page triggers an async check via the API.
func (s *Server) subStatesSnapshot() []map[string]any {
	out := make([]map[string]any, 0)
	if s.subs == nil {
		return out
	}
	for _, e := range s.subs.List() {
		synced := ""
		if e.UpdatedAt != nil {
			synced = e.UpdatedAt.Format("2006-01-02 15:04")
		}
		out = append(out, map[string]any{
			"workshop_id": e.WorkshopID,
			"app_id":      e.AppID,
			"title":       e.Title,
			"manifest":    e.ManifestGID,
			"synced":      synced,
			"downloaded":  e.ManifestGID != 0,
		})
	}
	return out
}

// apiSubsList returns subscriptions plus live update state from Steam.
func (s *Server) apiSubsList(w http.ResponseWriter, r *http.Request) {
	if s.subs == nil {
		writeJSON(w, 200, map[string]any{"items": []any{}})
		return
	}
	val, err := s.browse.CallCtx(r.Context(), workerWait,
		func(ctx context.Context, sess *steam.Session, _ *cdn.Client) (any, error) {
			return subscription.CheckUpdates(ctx, s.subs, sess.CM, s.log)
		})
	if err != nil {
		writeErr(w, 502, fmt.Sprintf("check updates: %v", err))
		return
	}
	states, _ := val.([]subscription.UpdateState)
	items := make([]map[string]any, 0, len(states))
	for _, st := range states {
		items = append(items, map[string]any{
			"workshop_id": st.Entry.WorkshopID,
			"app_id":      st.Entry.AppID,
			"title":       st.Entry.Title,
			"manifest":    st.Entry.ManifestGID,
			"remote_gid":  st.RemoteGID,
			"has_update":  st.HasUpdate || st.NeverSync,
			"never_sync":  st.NeverSync,
			"error":       st.CheckError,
			"synced":      formatTime(st.Entry.UpdatedAt),
		})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// apiSubsAdd subscribes to one item (resolves collections server-side).
func (s *Server) apiSubsAdd(w http.ResponseWriter, r *http.Request) {
	if s.subs == nil {
		writeErr(w, 500, "subscription store unavailable")
		return
	}
	var req struct {
		AppID      int    `json:"app_id"`
		WorkshopID uint64 `json:"workshop_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorkshopID == 0 {
		writeErr(w, 400, "invalid body: need app_id + workshop_id")
		return
	}
	val, err := s.browse.CallCtx(r.Context(), workerWait,
		func(ctx context.Context, sess *steam.Session, _ *cdn.Client) (any, error) {
			ids, err := workshop.ResolveIDs(ctx, sess.CM, []uint64{req.WorkshopID}, s.log)
			if err != nil {
				return nil, err
			}
			items, _ := workshop.GetDetails(ctx, sess.CM, ids, false, s.log)
			titles := make(map[uint64]string, len(items))
			for _, it := range items {
				titles[it.ID] = it.Title
			}
			added := 0
			for _, id := range ids {
				t := titles[id]
				if t == "" {
					t = strconv.FormatUint(id, 10)
				}
				if err := s.subs.Add(subscription.Entry{WorkshopID: id, AppID: uint32(req.AppID), Title: t}); err != nil {
					return nil, err
				}
				added++
			}
			return added, nil
		})
	if err != nil {
		writeErr(w, 502, fmt.Sprintf("subscribe: %v", err))
		return
	}
	writeJSON(w, 200, map[string]any{"added": val})
}

// apiSubsRemove unsubscribes one item.
func (s *Server) apiSubsRemove(w http.ResponseWriter, r *http.Request) {
	if s.subs == nil {
		writeErr(w, 500, "subscription store unavailable")
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeErr(w, 400, "invalid id")
		return
	}
	if err := s.subs.Remove(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"removed": id})
}

// apiSubsUpdate incrementally syncs subscriptions (all pending, or one id).
// Runs on the download worker; responds when finished (can take a while).
func (s *Server) apiSubsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.subs == nil {
		writeErr(w, 500, "subscription store unavailable")
		return
	}
	var ids []uint64
	if raw := r.URL.Query().Get("id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			writeErr(w, 400, "invalid id")
			return
		}
		ids = []uint64{id}
	}
	val, err := s.download.CallWithCDN(0, // no client-side timeout; big updates are fine
		func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error) {
			return subscription.Sync(ctx, s.subs, sess.CM, cdnClient, s.cfg.OutputDir, ids, s.log, download.Options{
				Retries: s.cfg.Retries,
				Jobs:    s.cfg.FileJobs,
			}), nil
		})
	if err != nil {
		writeErr(w, 502, fmt.Sprintf("sync: %v", err))
		return
	}
	results, _ := val.([]subscription.SyncResult)
	out := make([]map[string]any, 0, len(results))
	var updated, skipped, failed int
	for _, r := range results {
		item := map[string]any{
			"workshop_id": r.WorkshopID,
			"title":       r.Title,
			"skipped":     r.Skipped,
			"removed":     r.Removed,
		}
		switch {
		case r.Err != nil:
			failed++
			item["error"] = r.Err.Error()
		case r.Skipped:
			skipped++
		default:
			updated++
			item["files_ok"] = r.Stats.OK
			item["files_skipped"] = r.Stats.Skipped
			item["bytes"] = r.Stats.BytesDone
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{
		"results": out,
		"updated": updated,
		"skipped": skipped,
		"failed":  failed,
	})
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}
