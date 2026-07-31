// Package web implements the SWD Workshop browse / search / download UI.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

const (
	defaultQueryPageSize = 20
	maxQueryPageSize     = 50
)

// Config for the web server.
type Config struct {
	Host      string
	Port      int
	OutputDir string
	ProxyURL  string
	Log       *ui.Log
}

// Server is the HTTP application.
type Server struct {
	cfg       Config
	log       *ui.Log
	browse    *SteamSession
	download  *SteamSession
	dlMgr     *DownloadManager
	cache     *BrowseCache
	templates map[string]*template.Template
	mux       *http.ServeMux
	httpSrv   *http.Server
}

// New builds the server (does not listen yet).
func New(cfg Config) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 8765
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "."
	}
	absOut, err := absPath(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	cfg.OutputDir = absOut
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("output dir: %w", err)
	}

	log := cfg.Log
	if log == nil {
		log, _ = ui.New(ui.Options{})
	}

	s := &Server{
		cfg:    cfg,
		log:    log,
		cache:  NewBrowseCache(),
		browse: NewSteamSession(cfg.ProxyURL, log, "browse"),
	}
	s.download = NewSteamSession(cfg.ProxyURL, log, "download")
	s.dlMgr = NewDownloadManager(s.download, cfg.OutputDir, log, 5)

	tmpls, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s.templates = tmpls
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
}

func absPath(p string) (string, error) {
	return confAbsImpl(p)
}

// Warmup logs into Steam on browse + download sessions.
func (s *Server) Warmup() {
	s.browse.Warmup()
	s.download.Warmup()
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe starts the HTTP server (blocking).
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if s.log != nil {
		s.log.Stage("WEB", fmt.Sprintf("Starting %s web UI on http://%s", version.AppName, addr))
		s.log.Dim("  Download dir: " + s.cfg.OutputDir)
		if s.cfg.ProxyURL != "" {
			s.log.Info("  Proxy: configured")
		}
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	if s.httpSrv != nil {
		err = s.httpSrv.Shutdown(ctx)
	}
	s.dlMgr.Close()
	s.browse.Close()
	s.download.Close()
	return err
}

func (s *Server) routes() {
	staticSub, err := fs.Sub(embeddedStatic, "static")
	if err == nil {
		s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}

	s.mux.HandleFunc("GET /api/health", s.apiHealth)
	s.mux.HandleFunc("GET /api/workshop", s.apiWorkshopList)
	s.mux.HandleFunc("GET /api/workshop/{id}", s.apiWorkshopDetail)
	s.mux.HandleFunc("POST /api/downloads", s.apiDownloadsCreate)
	s.mux.HandleFunc("GET /api/downloads", s.apiDownloadsList)
	s.mux.HandleFunc("GET /api/downloads/{id}", s.apiDownloadsOne)
	s.mux.HandleFunc("GET /media/image", s.mediaImage)
	s.mux.HandleFunc("GET /{$}", s.indexPage)
	s.mux.HandleFunc("GET /item/{id}", s.itemPage)
	s.mux.HandleFunc("GET /downloads", s.downloadsPage)
}

func (s *Server) baseData() map[string]any {
	counts := s.dlMgr.Counts()
	active := counts["queued"] + counts["running"]
	var activePtr any
	if active > 0 {
		activePtr = active
	}
	return map[string]any{
		"DownloadEnabled": true,
		"DownloadOutput":  s.cfg.OutputDir,
		"DownloadCounts":  counts,
		"ActiveDownloads": activePtr,
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	t := s.templates[name]
	if t == nil {
		http.Error(w, "template not found: "+name, 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("template %s: %v", name, err))
		}
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]any{"error": msg, "description": msg})
}

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, 200, map[string]any{
		"ok":              true,
		"steam_logged_on": s.browse.LoggedOn(),
		"steam_client":    s.browse.HasClient(),
		"proxy":           s.cfg.ProxyURL != "",
		"download_enabled": true,
		"output_dir":      s.cfg.OutputDir,
		"downloads":       s.dlMgr.Counts(),
		"version":         version.Version,
	})
}

func (s *Server) parseListArgs(r *http.Request) (appID int, q string, page, perPage int, sort, cursor string, err error) {
	appRaw := r.URL.Query().Get("appid")
	if appRaw == "" {
		return 0, "", 0, 0, "", "", fmt.Errorf("missing required query parameter: appid")
	}
	appID, err = strconv.Atoi(appRaw)
	if err != nil || appID <= 0 {
		return 0, "", 0, 0, "", "", fmt.Errorf("appid must be a positive integer")
	}
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("search"))
	}
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = defaultQueryPageSize
	}
	if perPage > maxQueryPageSize {
		perPage = maxQueryPageSize
	}
	sort = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	cursor = strings.TrimSpace(r.URL.Query().Get("cursor"))
	return appID, q, page, perPage, sort, cursor, nil
}

func (s *Server) runQuery(ctx context.Context, appID int, q string, page, perPage int, sort, cursor string) (workshop.QueryResult, error) {
	sortKey := sort
	if sortKey == "" {
		if q != "" {
			sortKey = "search"
		} else {
			sortKey = "trend"
		}
	}
	queryType := workshop.ResolveQueryType(sort, q)
	useCursor := s.cache.CursorForPage(appID, q, sortKey, page, perPage, cursor)
	key := QueryKey(appID, q, sortKey, page, perPage, useCursor)
	if cached, ok := s.cache.GetQuery(key); ok {
		return cached, nil
	}

	val, err := s.browse.Call(workerWait, func(ctx context.Context, sess *steam.Session) (any, error) {
		return workshop.QueryFiles(ctx, sess.CM, appID, q, page, perPage, queryType, useCursor, s.log)
	})
	if err != nil {
		return workshop.QueryResult{}, err
	}
	result := val.(workshop.QueryResult)
	s.cache.PutQuery(key, result)
	return result, nil
}

func (s *Server) runDetail(ctx context.Context, itemID uint64) (*workshop.Item, error) {
	if it, ok := s.cache.GetItem(itemID); ok {
		cp := it
		return &cp, nil
	}
	val, err := s.browse.Call(workerWait, func(ctx context.Context, sess *steam.Session) (any, error) {
		return workshop.GetItemDetails(ctx, sess.CM, itemID, s.log)
	})
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	item := val.(*workshop.Item)
	if item != nil {
		s.cache.PutItem(*item)
	}
	return item, nil
}

func (s *Server) apiWorkshopList(w http.ResponseWriter, r *http.Request) {
	appID, q, page, perPage, sort, cursor, err := s.parseListArgs(r)
	if err != nil {
		s.writeErr(w, 400, err.Error())
		return
	}
	result, err := s.runQuery(r.Context(), appID, q, page, perPage, sort, cursor)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("QueryFiles failed: %v", err))
		}
		s.writeErr(w, 502, fmt.Sprintf("Steam QueryFiles failed: %v", err))
		return
	}
	s.writeJSON(w, 200, QueryToDict(result, s.cfg.ProxyURL))
}

func (s *Server) apiWorkshopDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		s.writeErr(w, 400, "invalid item id")
		return
	}
	item, err := s.runDetail(r.Context(), id)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("GetDetails failed for %d: %v", id, err))
		}
		s.writeErr(w, 502, fmt.Sprintf("Steam GetDetails failed: %v", err))
		return
	}
	if item == nil {
		s.writeErr(w, 404, fmt.Sprintf("Workshop item %d not found", id))
		return
	}
	s.writeJSON(w, 200, ItemToDict(*item, s.cfg.ProxyURL))
}

func (s *Server) apiDownloadsCreate(w http.ResponseWriter, r *http.Request) {
	var appID int
	var workshopID uint64
	var title string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, 400, "invalid JSON body")
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
		s.writeErr(w, 400, "appid and workshopid must be positive integers")
		return
	}
	jobs := s.dlMgr.Enqueue(appID, workshopID, title)
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ToDict())
	}
	s.writeJSON(w, 202, map[string]any{
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
	s.writeJSON(w, 200, map[string]any{
		"output_dir": s.cfg.OutputDir,
		"counts":     s.dlMgr.Counts(),
		"jobs":       out,
	})
}

func (s *Server) apiDownloadsOne(w http.ResponseWriter, r *http.Request) {
	job := s.dlMgr.GetJob(r.PathValue("id"))
	if job == nil {
		s.writeErr(w, 404, "job not found")
		return
	}
	s.writeJSON(w, 200, job.ToDict())
}

func (s *Server) mediaImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" {
		raw = r.URL.Query().Get("url")
	}
	if raw == "" {
		s.writeErr(w, 400, "missing u= image url")
		return
	}
	body, ctype, err := FetchMedia(s.browse.HTTP(), raw)
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
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	data := s.baseData()
	appidRaw := r.URL.Query().Get("appid")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sort := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = defaultQueryPageSize
	}
	if perPage > maxQueryPageSize {
		perPage = maxQueryPageSize
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	data["AppID"] = appidRaw
	data["Q"] = q
	data["Page"] = page
	data["PerPage"] = perPage
	data["Cursor"] = cursor
	data["HasQuery"] = strings.TrimSpace(appidRaw) != ""
	data["ProxyEnabled"] = s.cfg.ProxyURL != ""
	data["Items"] = []ItemDict{}
	data["Total"] = 0
	data["TotalPages"] = 1
	data["NextCursor"] = ""
	data["Error"] = ""

	sortEffective := sort
	if sortEffective == "" {
		if q != "" {
			sortEffective = "search"
		} else {
			sortEffective = "trend"
		}
	}
	data["Sort"] = sortEffective

	if strings.TrimSpace(appidRaw) != "" {
		appID, err := strconv.Atoi(appidRaw)
		if err != nil || appID <= 0 {
			data["Error"] = "AppID must be a positive integer."
		} else {
			result, err := s.runQuery(r.Context(), appID, q, page, perPage, sort, cursor)
			if err != nil {
				if s.log != nil {
					s.log.Err(fmt.Sprintf("QueryFiles failed: %v", err))
				}
				data["Error"] = fmt.Sprintf("Steam query failed: %v", err)
			} else {
				items := make([]ItemDict, 0, len(result.Items))
				for _, it := range result.Items {
					items = append(items, ItemToDict(it, s.cfg.ProxyURL))
				}
				totalPages := 1
				if result.Total > 0 {
					totalPages = (result.Total + perPage - 1) / perPage
					if totalPages < 1 {
						totalPages = 1
					}
				}
				data["Items"] = items
				data["Total"] = result.Total
				data["TotalPages"] = totalPages
				data["NextCursor"] = result.NextCursor
				data["Page"] = result.Page
			}
		}
	}
	s.render(w, "index.html", data)
}

func (s *Server) itemPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "invalid item id", 400)
		return
	}
	data := s.baseData()
	data["ItemID"] = id
	data["AppID"] = r.URL.Query().Get("appid")
	data["ProxyEnabled"] = s.cfg.ProxyURL != ""
	data["Error"] = ""
	data["Item"] = nil

	item, err := s.runDetail(r.Context(), id)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("GetDetails failed for %d: %v", id, err))
		}
		data["Error"] = fmt.Sprintf("Steam GetDetails failed: %v", err)
	} else if item == nil {
		data["Error"] = fmt.Sprintf("Workshop item %d not found.", id)
	} else {
		data["Item"] = ItemToDict(*item, s.cfg.ProxyURL)
	}
	s.render(w, "detail.html", data)
}

func (s *Server) downloadsPage(w http.ResponseWriter, r *http.Request) {
	data := s.baseData()
	jobs := s.dlMgr.ListJobs(100)
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ToDict())
	}
	data["Jobs"] = out
	data["Counts"] = s.dlMgr.Counts()
	s.render(w, "downloads.html", data)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"trunc": func(s string, n int) string {
			if n <= 0 || len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
	}
}

var (
	tmplOnce sync.Once
	tmplErr  error
	tmplMap  map[string]*template.Template
)

func parseTemplates() (map[string]*template.Template, error) {
	tmplOnce.Do(func() {
		pages := []string{"index.html", "detail.html", "downloads.html"}
		tmplMap = make(map[string]*template.Template, len(pages))
		for _, page := range pages {
			t, err := template.New("base").Funcs(templateFuncs()).ParseFS(embeddedTemplates, "templates/base.html", "templates/"+page)
			if err != nil {
				tmplErr = fmt.Errorf("parse %s: %w", page, err)
				return
			}
			tmplMap[page] = t
		}
	})
	return tmplMap, tmplErr
}

func anyToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	default:
		return 0
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
