package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

// DownloadCmd is the phase-1 download subcommand.
type DownloadCmd struct {
	AppID      int     `arg:"" name:"appid" help:"Steam AppID (e.g. 294100 for RimWorld)"`
	WorkshopID []int64 `arg:"" name:"workshopid" help:"One or more Workshop published-file IDs"`
	Output     string  `short:"o" default:"." help:"Output directory"`
	Verbose    bool    `short:"v" help:"Per-file progress lines"`
	Proxy      string  `help:"Proxy URL: socks5://, socks4://, http(s)://; bare host:port => socks5. Optional user:pass@. Needs full TCP (CM :27017), not HTTPS-only."`
	Retries    int     `default:"5" help:"Per-file retry attempts on failure"`
	Jobs       int     `short:"j" default:"0" help:"Parallel file downloads per item (0=auto cap 8, 1=serial)"`
	Color      bool    `help:"Force coloured output"`
	NoColor    bool    `help:"Disable ANSI colour"`
	LogFile    string  `type:"path" help:"Tee human output (ANSI stripped) to this file"`
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
	log.Dim(fmt.Sprintf("  AppID: %d, IDs: %s", c.AppID, joinComma(ids)))
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	sess, err := steam.InitSession(ctx, c.Proxy, log)
	if err != nil {
		return fmt.Errorf("failed to initialize session: %w", err)
	}
	defer sess.Close()

	want := make([]uint64, len(c.WorkshopID))
	for i, id := range c.WorkshopID {
		want[i] = uint64(id)
	}
	resolved, err := workshop.ResolveIDs(ctx, sess.CM, want, log)
	if err != nil {
		log.Err(fmt.Sprintf("resolve: %v", err))
	}
	if len(resolved) == 0 {
		log.Warn("No valid workshop items to download")
		return fmt.Errorf("no items")
	}

	log.Info(fmt.Sprintf("\n%d item%s to download", len(resolved), plural(len(resolved))))

	cdnClient := cdn.New(sess.CM, sess.HTTP(), log)
	if err := cdnClient.EnsureServers(ctx); err != nil {
		return fmt.Errorf("content servers: %w", err)
	}

	runStart := time.Now()
	var totalOK, totalFail, totalSkip int
	var totalBytes int64
	failedItems := 0

	for idx, wid := range resolved {
		log.Stage("ITEM", fmt.Sprintf("[%d/%d] %d", idx+1, len(resolved), wid))
		stats := download.DownloadItem(ctx, cdnClient, wid, outDir, log, download.Options{
			Retries: c.Retries,
			Jobs:    c.Jobs,
			Verbose: c.Verbose,
		})
		if stats.FullyFailed() {
			failedItems++
		} else {
			totalOK += stats.OK
			totalFail += stats.Fail
			totalSkip += stats.Skipped
			totalBytes += stats.BytesDone
		}
	}

	duration := time.Since(runStart)
	log.Blank()
	rule := "--------------------------------------------------"
	log.Info(rule)
	log.Info(fmt.Sprintf(
		"  Items:  %d total / %d ok / %d failed",
		len(resolved), len(resolved)-failedItems, failedItems,
	))
	filesTotal := totalOK + totalFail + totalSkip
	filesParts := []string{fmt.Sprintf("%d ok", totalOK)}
	if totalSkip > 0 {
		filesParts = append(filesParts, fmt.Sprintf("%d skipped", totalSkip))
	}
	filesParts = append(filesParts, fmt.Sprintf("%d failed", totalFail))
	log.Info(fmt.Sprintf(
		"  Files:  %d total / %s  (%s downloaded)",
		filesTotal, joinSlash(filesParts), util.FormatSize(totalBytes),
	))
	log.Info(fmt.Sprintf("  Duration: %s", util.FormatDuration(duration.Seconds())))
	if totalBytes > 0 && duration > 0 {
		avg := (float64(totalBytes) / (1024 * 1024)) / duration.Seconds()
		log.Info(fmt.Sprintf("  Average:  %.1f MB/s", avg))
	} else {
		log.Info("  Average:  n/a")
	}
	log.Info(rule)

	if failedItems > 0 {
		return fmt.Errorf("%d item(s) failed", failedItems)
	}
	log.Info("All items downloaded.")
	return nil
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

func joinSlash(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for i := 1; i < len(ss); i++ {
		out += " / " + ss[i]
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
