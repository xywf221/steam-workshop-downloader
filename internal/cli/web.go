package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
	"github.com/xywf221/steam-workshop-downloader/internal/web"
)

// WebCmd runs the Workshop browse / download web UI.
type WebCmd struct {
	Host    string `default:"127.0.0.1" help:"Bind host"`
	Port    int    `default:"8765" help:"Bind port"`
	Output  string `short:"o" default:"." help:"Download directory for web-triggered jobs"`
	Proxy   string `help:"Optional proxy URL (socks5/socks4/http); needs full TCP for Steam CM :27017"`
	Jobs    int    `short:"j" default:"0" help:"Parallel file downloads per item (0=auto cap 8, 1=serial)"`
	Retries int    `default:"5" help:"Per-file retry attempts on failure"`
}

// Run implements kong Runner.
func (c *WebCmd) Run() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	outDir, err := filepath.Abs(c.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("cannot create output directory %s: %w", outDir, err)
	}

	log, err := ui.New(ui.Options{})
	if err != nil {
		return err
	}
	defer log.Close()

	log.Info(fmt.Sprintf("=== %s v%s web ===", version.AppName, version.Version))
	if c.Proxy != "" {
		p, err := proxy.Parse(c.Proxy)
		if err != nil {
			return fmt.Errorf("invalid --proxy: %w", err)
		}
		auth := "auth=no"
		if p.HasAuth || p.Username != "" {
			auth = "auth=yes"
		}
		log.Info(fmt.Sprintf("Proxy: %s (%s)", p.RedactedURL(), auth))
	}

	if c.Retries < 1 {
		return fmt.Errorf("--retries must be >= 1")
	}
	srv, err := web.New(web.Config{
		Host:      c.Host,
		Port:      c.Port,
		OutputDir: outDir,
		ProxyURL:  c.Proxy,
		Log:       log,
		FileJobs:  c.Jobs,
		Retries:   c.Retries,
	})
	if err != nil {
		return err
	}

	// Eager Steam login (browse + download sessions).
	go srv.Warmup()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err.Error() != "http: Server closed" {
			return err
		}
		return nil
	case sig := <-sigCh:
		log.Info(fmt.Sprintf("Shutting down (%v)...", sig))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return nil
	}
}
