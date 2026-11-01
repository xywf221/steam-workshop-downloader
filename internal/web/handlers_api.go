package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"ok":               true,
		"steam_logged_on":  s.browse.LoggedOn(),
		"steam_client":     s.browse.HasClient(),
		"proxy":            s.cfg.ProxyURL != "",
		"download_enabled": true,
		"output_dir":       s.cfg.OutputDir,
		"downloads":        s.dlMgr.Counts(),
		"version":          version.Version,
	})
}

func (s *Server) apiWorkshopList(w http.ResponseWriter, r *http.Request) {
	appID, q, page, perPage, sort, cursor, lang, err := s.parseListArgs(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	result, err := s.runQuery(r.Context(), appID, q, page, perPage, sort, cursor, lang)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("QueryFiles failed: %v", err))
		}
		writeErr(w, 502, fmt.Sprintf("Steam QueryFiles failed: %v", err))
		return
	}
	writeJSON(w, 200, QueryToDict(result, s.cfg.ProxyURL))
}

func (s *Server) apiWorkshopDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeErr(w, 400, "invalid item id")
		return
	}
	lang, _ := strconv.Atoi(r.URL.Query().Get("lang"))
	item, err := s.runDetail(r.Context(), id, lang)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("GetDetails failed for %d: %v", id, err))
		}
		writeErr(w, 502, fmt.Sprintf("Steam GetDetails failed: %v", err))
		return
	}
	if item == nil {
		writeErr(w, 404, fmt.Sprintf("Workshop item %d not found", id))
		return
	}
	writeJSON(w, 200, ItemToDict(*item, s.cfg.ProxyURL))
}

func (s *Server) apiDownloadsCreate(w http.ResponseWriter, r *http.Request) {
	var appID int
	var workshopID uint64
	var title string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil {
			writeErr(w, 400, "invalid JSON body")
			return
		}
		appID = anyToInt(body["appid"])
		workshopID = uint64(anyToInt(body["workshopid"]))
		if workshopID == 0 {
			workshopID = uint64(anyToInt(body["id"]))
		}
		if t, ok := body["title"].(string); ok {
			title = t
		}
	} else {
		_ = r.ParseForm()
		appID, _ = strconv.Atoi(firstNonEmpty(r.Form.Get("appid"), r.URL.Query().Get("appid")))
		wid, _ := strconv.ParseUint(firstNonEmpty(r.Form.Get("workshopid"), r.URL.Query().Get("workshopid")), 10, 64)
		workshopID = wid
		title = firstNonEmpty(r.Form.Get("title"), r.URL.Query().Get("title"))
	}
	if appID <= 0 || workshopID == 0 {
		writeErr(w, 400, "appid and workshopid must be positive integers")
		return
	}
	jobs := s.dlMgr.Enqueue(appID, workshopID, title)
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ToDict())
	}
	writeJSON(w, 202, map[string]any{
		"ok":         true,
		"jobs":       out,
		"output_dir": s.cfg.OutputDir,
	})
}

func (s *Server) apiDownloadsList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	jobs := s.dlMgr.ListJobs(limit)
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ToDict())
	}
	writeJSON(w, 200, map[string]any{
		"output_dir": s.cfg.OutputDir,
		"counts":     s.dlMgr.Counts(),
		"jobs":       out,
	})
}

func (s *Server) apiDownloadsOne(w http.ResponseWriter, r *http.Request) {
	job := s.dlMgr.GetJob(r.PathValue("id"))
	if job == nil {
		writeErr(w, 404, "job not found")
		return
	}
	writeJSON(w, 200, job.ToDict())
}

func (s *Server) mediaImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" {
		raw = r.URL.Query().Get("url")
	}
	if raw == "" {
		writeErr(w, 400, "missing u= image url")
		return
	}
	body, ctype, err := s.media.Fetch(s.browse.HTTP(), raw)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not allowed") || strings.Contains(msg, "empty") ||
			strings.Contains(msg, "scheme") || strings.Contains(msg, "invalid host") {
			http.Error(w, msg, 400)
			return
		}
		if s.log != nil {
			s.log.Warn(fmt.Sprintf("media fetch failed: %v", err))
		}
		http.Error(w, fmt.Sprintf("media fetch failed: %v", err), 502)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
