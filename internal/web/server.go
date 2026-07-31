// Package web implements the SWD Workshop browse / search / download UI.
package web

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
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
	// FileJobs is parallel file downloads per item inside the web queue (0 = auto, cap 8).
	FileJobs int
	Retries  int
}

// Server is the HTTP application.
type Server struct {
	cfg       Config
	log       *ui.Log
	browse    *SteamSession
	download  *SteamSession
	dlMgr     *DownloadManager
	cache     *BrowseCache
	media     *MediaCache
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
	if cfg.Retries < 1 {
		cfg.Retries = 5
	}
	absOut, err := confAbsImpl(cfg.OutputDir)
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
		media:  NewMediaCache(),
		browse: NewSteamSession(cfg.ProxyURL, log, "browse"),
	}
	s.download = NewSteamSession(cfg.ProxyURL, log, "download")
	s.dlMgr = NewDownloadManager(s.download, cfg.OutputDir, log, cfg.Retries, cfg.FileJobs)

	tmpls, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s.templates = tmpls
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
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
		IdleTimeout:       60 * time.Second,
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
		fileServer := http.FileServer(http.FS(staticSub))
		s.mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			http.StripPrefix("/static/", fileServer).ServeHTTP(w, r)
		}))
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

	val, err := s.browse.CallCtx(ctx, workerWait, func(ctx context.Context, sess *steam.Session, _ *cdn.Client) (any, error) {
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
	val, err := s.browse.CallCtx(ctx, workerWait, func(ctx context.Context, sess *steam.Session, _ *cdn.Client) (any, error) {
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
