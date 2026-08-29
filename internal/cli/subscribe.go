package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/subscription"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
)

// SubCmd groups subscription management subcommands.
type SubCmd struct {
	Add    SubAddCmd    `cmd:"" help:"Subscribe to workshop items"`
	List   SubListCmd   `cmd:"" name:"list" help:"List subscriptions"`
	Remove SubRemoveCmd `cmd:"" name:"remove" aliases:"rm" help:"Unsubscribe (keeps downloaded files)"`
	Check  SubCheckCmd  `cmd:"" help:"Check subscriptions for updates (no download)"`
	Update SubUpdateCmd `cmd:"" help:"Incrementally download pending updates"`
}

type subCommon struct {
	Output  string `short:"o" default:"." help:"Output directory (where subscriptions.json lives)"`
	Proxy   string `help:"Proxy URL (see download --proxy)"`
	NoColor bool   `help:"Disable ANSI colour"`
}

func (c *subCommon) outDir() (string, error) {
	d, err := filepath.Abs(c.Output)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

func (c *subCommon) logger() (*ui.Log, error) {
	var colorPtr *bool
	if c.NoColor {
		f := false
		colorPtr = &f
	}
	return ui.New(ui.Options{Color: colorPtr})
}

// session opens a Steam session + CDN client for sub commands.
func (c *subCommon) session(ctx context.Context, log *ui.Log) (*steam.Session, *cdn.Client, error) {
	sess, err := steam.InitSession(ctx, c.Proxy, log)
	if err != nil {
		return nil, nil, err
	}
	return sess, cdn.New(sess.CM, sess.HTTP(), log), nil
}

// SubAddCmd subscribes to one or more workshop items.
type SubAddCmd struct {
	AppID      int     `arg:"" name:"appid" help:"Steam AppID"`
	WorkshopID []int64 `arg:"" name:"workshopid" help:"Workshop published-file IDs (collections expand to their items)"`
	subCommon
}

func (c *SubAddCmd) Run() error {
	if c.AppID <= 0 || len(c.WorkshopID) == 0 {
		return fmt.Errorf("appid and at least one workshopid are required")
	}
	log, err := c.logger()
	if err != nil {
		return err
	}
	defer log.Close()
	outDir, err := c.outDir()
	if err != nil {
		return err
	}
	store, err := subscription.Load(outDir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sess, err := steam.InitSession(ctx, c.Proxy, log)
	if err != nil {
		return err
	}
	defer sess.Close()

	want := make([]uint64, len(c.WorkshopID))
	for i, id := range c.WorkshopID {
		want[i] = uint64(id)
	}
	resolved, err := workshop.ResolveIDs(ctx, sess.CM, want, log)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	items, err := workshop.GetDetails(ctx, sess.CM, resolved, false, log)
	if err != nil {
		return err
	}
	titles := make(map[uint64]string, len(items))
	for _, it := range items {
		titles[it.ID] = it.Title
	}
	for _, id := range resolved {
		t := titles[id]
		if t == "" {
			t = fmt.Sprintf("%d", id)
		}
		if err := store.Add(subscription.Entry{WorkshopID: id, AppID: uint32(c.AppID), Title: t}); err != nil {
			return err
		}
		log.OK(fmt.Sprintf("subscribed: %s (%d)", t, id))
	}
	log.Info(fmt.Sprintf("%d subscription(s) total", len(store.List())))
	return nil
}

// SubListCmd prints all subscriptions.
type SubListCmd struct {
	subCommon
}

func (c *SubListCmd) Run() error {
	outDir, err := c.outDir()
	if err != nil {
		return err
	}
	store, err := subscription.Load(outDir)
	if err != nil {
		return err
	}
	entries := store.List()
	if len(entries) == 0 {
		fmt.Println("No subscriptions. Add with: swd sub add <appid> <workshopid...>")
		return nil
	}
	for _, e := range entries {
		state := "not downloaded"
		if e.ManifestGID != 0 {
			state = fmt.Sprintf("manifest %d", e.ManifestGID)
			if e.UpdatedAt != nil {
				state += ", synced " + e.UpdatedAt.Format("2006-01-02 15:04")
			}
		}
		fmt.Printf("%-14d %-6d %-40s %s\n", e.WorkshopID, e.AppID, trunc(e.Title, 40), state)
	}
	return nil
}

// SubRemoveCmd unsubscribes (downloaded files are left alone).
type SubRemoveCmd struct {
	WorkshopID []int64 `arg:"" name:"workshopid" help:"Workshop IDs to unsubscribe"`
	subCommon
}

func (c *SubRemoveCmd) Run() error {
	if len(c.WorkshopID) == 0 {
		return fmt.Errorf("at least one workshopid is required")
	}
	outDir, err := c.outDir()
	if err != nil {
		return err
	}
	store, err := subscription.Load(outDir)
	if err != nil {
		return err
	}
	for _, id := range c.WorkshopID {
		if err := store.Remove(uint64(id)); err != nil {
			return err
		}
		fmt.Printf("unsubscribed: %d\n", id)
	}
	return nil
}

// SubCheckCmd reports which subscriptions have updates.
type SubCheckCmd struct {
	subCommon
}

func (c *SubCheckCmd) Run() error {
	log, err := c.logger()
	if err != nil {
		return err
	}
	defer log.Close()
	outDir, err := c.outDir()
	if err != nil {
		return err
	}
	store, err := subscription.Load(outDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sess, err := steam.InitSession(ctx, c.Proxy, log)
	if err != nil {
		return err
	}
	defer sess.Close()

	states, err := subscription.CheckUpdates(ctx, store, sess.CM, log)
	if err != nil {
		return err
	}
	pending := 0
	for _, st := range states {
		switch {
		case st.CheckError != "":
			fmt.Printf("%-14d %-40s ? %s\n", st.Entry.WorkshopID, trunc(st.Entry.Title, 40), st.CheckError)
		case st.NeverSync:
			pending++
			fmt.Printf("%-14d %-40s NEW (never downloaded)\n", st.Entry.WorkshopID, trunc(st.Entry.Title, 40))
		case st.HasUpdate:
			pending++
			fmt.Printf("%-14d %-40s UPDATE %d -> %d\n", st.Entry.WorkshopID, trunc(st.Entry.Title, 40), st.Entry.ManifestGID, st.RemoteGID)
		default:
			fmt.Printf("%-14d %-40s up to date\n", st.Entry.WorkshopID, trunc(st.Entry.Title, 40))
		}
	}
	fmt.Printf("\n%d subscription(s), %d pending\n", len(states), pending)
	return nil
}

// SubUpdateCmd downloads pending subscription updates incrementally.
type SubUpdateCmd struct {
	WorkshopID []int64 `arg:"" optional:"" name:"workshopid" help:"Limit to these IDs (default: all with updates)"`
	Retries    int     `default:"5" help:"Per-file retry attempts"`
	Jobs       int     `short:"j" default:"0" help:"Parallel file downloads per item"`
	Verbose    bool    `short:"v" help:"Per-file progress lines"`
	subCommon
}

func (c *SubUpdateCmd) Run() error {
	log, err := c.logger()
	if err != nil {
		return err
	}
	defer log.Close()
	outDir, err := c.outDir()
	if err != nil {
		return err
	}
	store, err := subscription.Load(outDir)
	if err != nil {
		return err
	}
	if len(store.List()) == 0 {
		fmt.Println("No subscriptions. Add with: swd sub add <appid> <workshopid...>")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	sess, cdnClient, err := c.session(ctx, log)
	if err != nil {
		return err
	}
	defer sess.Close()

	var ids []uint64
	for _, id := range c.WorkshopID {
		ids = append(ids, uint64(id))
	}
	results := subscription.Sync(ctx, store, sess.CM, cdnClient, outDir, ids, log, download.Options{
		Retries: c.Retries,
		Jobs:    c.Jobs,
		Verbose: c.Verbose,
	})

	var updated, skipped, failed int
	var bytes int64
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed++
			log.Err(fmt.Sprintf("%d (%s): %v", r.WorkshopID, r.Title, r.Err))
		case r.Skipped:
			skipped++
		default:
			updated++
			bytes += r.Stats.BytesDone
			log.OK(fmt.Sprintf("%s: %d files updated, %d stale removed", r.Title, r.Stats.OK, len(r.Removed)))
		}
	}
	log.Info(fmt.Sprintf("\n%d updated / %d up-to-date / %d failed (%s downloaded)",
		updated, skipped, failed, util.FormatSize(bytes)))
	if failed > 0 {
		return fmt.Errorf("%d item(s) failed", failed)
	}
	return nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
