package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

// DownloadCmd is the phase-1 download subcommand.
//
// Steam protocol wiring lands in later milestones; for now we validate flags,
// set up logging, and report that the protocol stack is not ready yet — so the
// CLI surface is testable without a live CM.
type DownloadCmd struct {
	AppID      int      `arg:"" name:"appid" help:"Steam AppID (e.g. 294100 for RimWorld)"`
	WorkshopID []int64  `arg:"" name:"workshopid" help:"One or more Workshop published-file IDs"`
	Output     string   `short:"o" default:"." help:"Output directory"`
	Verbose    bool     `short:"v" help:"Per-file progress lines"`
	Proxy      string   `help:"Proxy URL: socks5://, socks4://, http(s)://; bare host:port => socks5. Optional user:pass@. Needs full TCP (CM :27017), not HTTPS-only."`
	Retries    int      `default:"5" help:"Per-file retry attempts on failure"`
	Jobs       int      `short:"j" default:"0" help:"Parallel file downloads per item (0=auto cap 8, 1=serial)"`
	Color      bool     `help:"Force coloured output"`
	NoColor    bool     `help:"Disable ANSI colour"`
	LogFile    string   `type:"path" help:"Tee human output (ANSI stripped) to this file"`
}

// Run implements kong Runner.
func (c *DownloadCmd) Run() error {
	if c.AppID <= 0 {
		return fmt.Errorf("appid must be a positive integer")
	}
	if len(c.WorkshopID) == 0 {
		return fmt.Errorf("at least one workshopid is required")
	}
	for _, id := range c.WorkshopID {
		if id <= 0 {
			return fmt.Errorf("workshopid must be positive, got %d", id)
		}
	}
	if c.Retries < 1 {
		return fmt.Errorf("--retries must be >= 1")
	}

	var colorPtr *bool
	switch {
	case c.NoColor:
		f := false
		colorPtr = &f
	case c.Color:
		t := true
		colorPtr = &t
	default:
		// auto TTY
		colorPtr = nil
	}

	log, err := ui.New(ui.Options{Color: colorPtr, LogFile: c.LogFile})
	if err != nil {
		return err
	}
	defer log.Close()

	outDir, err := filepath.Abs(c.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("cannot create output directory %s: %w", outDir, err)
	}

	log.Info(fmt.Sprintf("=== %s v%s ===", version.AppName, version.Version))
	ids := make([]string, len(c.WorkshopID))
	for i, id := range c.WorkshopID {
		ids[i] = fmt.Sprintf("%d", id)
	}
	log.Dim(fmt.Sprintf("  AppID: %d, IDs: %v", c.AppID, joinComma(ids)))
	if c.Proxy != "" {
		p, err := proxy.Parse(c.Proxy)
		if err != nil {
			return fmt.Errorf("invalid --proxy: %w", err)
		}
		auth := "auth=no"
		if p.HasAuth || p.Username != "" {
			auth = "auth=yes"
		}
		log.Dim(fmt.Sprintf("  Proxy: %s (%s)", p.RedactedURL(), auth))
	} else {
		log.Dim("  Proxy: (direct connection)")
	}
	log.Dim(fmt.Sprintf("  Output: %s", outDir))
	log.Dim(fmt.Sprintf("  Retries per file: %d", c.Retries))
	jobsLabel := fmt.Sprintf("auto (cap %d)", util.MaxAutoJobs)
	if c.Jobs > 0 {
		jobsLabel = fmt.Sprintf("%d", c.Jobs)
	}
	log.Dim(fmt.Sprintf("  Jobs per item: %s", jobsLabel))

	ctx, cancel := context.WithTimeout(context.Background(), steam.DefaultInitTimeout())
	defer cancel()

	sess, err := steam.InitSession(ctx, c.Proxy, log)
	if err != nil {
		return fmt.Errorf("failed to initialize session: %w", err)
	}
	defer sess.Close()

	// M3+ will resolve collections and download manifests/chunks.
	log.Warn("Steam anonymous login OK — workshop download pipeline lands in M3–M6")
	log.Dim(fmt.Sprintf("  steamid=%d cell=%d", sess.CM.SteamID(), sess.CM.CellID()))
	return fmt.Errorf("download not implemented yet (next: GetDetails + CDN); login works")
}

func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for i := 1; i < len(ss); i++ {
		out += ", " + ss[i]
	}
	return out
}
