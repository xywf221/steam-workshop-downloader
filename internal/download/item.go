// Package download implements per-item workshop file download with skip/retry/jobs.
package download

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/chunkcodec"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/manifest"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
)

// Stats summarises one workshop item download.
type Stats struct {
	OutDir    string
	Name      string
	OK        int
	Fail      int
	Skipped   int
	BytesDone int64
	Duration  time.Duration
}

// FullyFailed is true when nothing was downloaded or skipped.
func (s Stats) FullyFailed() bool {
	return s.OK == 0 && s.Skipped == 0 && s.Fail > 0
}

// Progress receives per-file events during DownloadItem (optional; for web UI).
type Progress interface {
	StartFiles(n int)
	FileOK(filename string, sizeBytes int64)
	FileSkip(filename string, sizeBytes int64)
	FileFail(filename string, err error)
	Retry(filename string, attempt, retries int, err error, backoffS int)
	EndFiles(stats Stats)
}

// Options for DownloadItem.
type Options struct {
	Retries  int // default 5
	Jobs     int // 0 = auto
	Verbose  bool
	Progress Progress // optional
}

// DownloadItem fetches one workshop item under outputDir/{workshopID}/.
func DownloadItem(
	ctx context.Context,
	cdnClient *cdn.Client,
	workshopID uint64,
	outputDir string,
	log *ui.Log,
	opts Options,
) Stats {
	t0 := time.Now()
	outDir := filepath.Join(outputDir, fmt.Sprintf("%d", workshopID))
	stats := Stats{OutDir: outDir, Name: fmt.Sprintf("%d", workshopID)}

	if opts.Retries < 1 {
		opts.Retries = 5
	}

	if log != nil {
		log.Stage("MANIFEST", fmt.Sprintf("Fetching manifest for %d...", workshopID))
	}
	m, err := cdnClient.ManifestForWorkshopItem(ctx, workshopID)
	if err != nil {
		if log != nil {
			log.Err(fmt.Sprintf("manifest for %d: %v", workshopID, err))
		}
		stats.Fail = 1
		stats.Duration = time.Since(t0)
		return stats
	}
	if m.Name != "" {
		stats.Name = m.Name
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		if log != nil {
			log.Err(err.Error())
		}
		stats.Fail = 1
		stats.Duration = time.Since(t0)
		return stats
	}

	type job struct {
		file manifest.File
		path string
	}
	var jobs []job
	for _, f := range m.Files {
		name := manifest.CleanName(f.Filename)
		if name == "." || name == "" {
			continue
		}
		p := filepath.Join(outDir, filepath.FromSlash(name))
		if f.IsDirectory() {
			_ = os.MkdirAll(p, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		jobs = append(jobs, job{file: f, path: p})
	}

	n := len(jobs)
	workers := opts.Jobs
	if workers <= 0 {
		workers = util.SuggestJobs(n, util.MaxAutoJobs)
	}
	if workers < 1 {
		workers = 1
	}
	if n > 0 && workers > n {
		workers = n
	}
	if log != nil {
		log.OK(fmt.Sprintf("'%s' - %d files, %d thread%s", stats.Name, n, workers, plural(workers)))
	}
	if opts.Progress != nil {
		opts.Progress.StartFiles(n)
	}

	var okN, failN, skipN int64
	var bytesDone int64

	runOne := func(j job) {
		st, nbytes, err := downloadOneFile(ctx, cdnClient, m, j.file, j.path, opts.Retries, log, opts.Verbose, opts.Progress)
		name := manifest.CleanName(j.file.Filename)
		switch st {
		case "downloaded":
			atomic.AddInt64(&okN, 1)
			atomic.AddInt64(&bytesDone, nbytes)
			if opts.Progress != nil {
				opts.Progress.FileOK(name, nbytes)
			}
		case "skipped":
			atomic.AddInt64(&skipN, 1)
			if opts.Progress != nil {
				opts.Progress.FileSkip(name, nbytes)
			}
		default:
			atomic.AddInt64(&failN, 1)
			if opts.Progress != nil {
				opts.Progress.FileFail(name, err)
			}
			if err != nil && log != nil && opts.Verbose {
				log.Err(fmt.Sprintf("%s: %v", j.file.Filename, err))
			}
		}
	}

	if n == 0 {
		stats.Duration = time.Since(t0)
		if opts.Progress != nil {
			opts.Progress.EndFiles(stats)
		}
		return stats
	}

	if workers == 1 {
		for _, j := range jobs {
			if ctx.Err() != nil {
				break
			}
			runOne(j)
		}
	} else {
		ch := make(chan job, n)
		for _, j := range jobs {
			ch <- j
		}
		close(ch)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range ch {
					if ctx.Err() != nil {
						return
					}
					runOne(j)
				}
			}()
		}
		wg.Wait()
	}

	stats.OK = int(okN)
	stats.Fail = int(failN)
	stats.Skipped = int(skipN)
	stats.BytesDone = bytesDone
	stats.Duration = time.Since(t0)
	if opts.Progress != nil {
		opts.Progress.EndFiles(stats)
	}
	return stats
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func downloadOneFile(
	ctx context.Context,
	cdnClient *cdn.Client,
	m *manifest.Manifest,
	f manifest.File,
	path string,
	retries int,
	log *ui.Log,
	verbose bool,
	prog Progress,
) (status string, nbytes int64, err error) {
	if localMatches(path, f) {
		fi, _ := os.Stat(path)
		n := int64(0)
		if fi != nil {
			n = fi.Size()
		}
		if verbose && log != nil {
			log.Dim(fmt.Sprintf("  skip %s", manifest.CleanName(f.Filename)))
		}
		return "skipped", n, nil
	}
	_ = os.Remove(path)

	var last error
	for attempt := 1; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return "failed", 0, ctx.Err()
		}
		data, err := cdnClient.ReadFile(ctx, m, &f, chunkcodec.Decompress)
		if err != nil {
			last = err
			if attempt < retries {
				wait := util.ComputeBackoff(attempt, 30)
				if log != nil {
					log.Retry(fmt.Sprintf("%s (%d/%d): %v; wait %ds", manifest.CleanName(f.Filename), attempt, retries, err, wait))
				}
				if prog != nil {
					prog.Retry(manifest.CleanName(f.Filename), attempt, retries, err, wait)
				}
				time.Sleep(time.Duration(wait) * time.Second)
				continue
			}
			break
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			last = err
			break
		}
		name := manifest.CleanName(f.Filename)
		if verbose && log != nil {
			log.Dim(fmt.Sprintf("  ok %s (%s)", name, util.FormatSize(int64(len(data)))))
		}
		return "downloaded", int64(len(data)), nil
	}
	_ = os.Remove(path)
	if last == nil {
		last = fmt.Errorf("download failed")
	}
	if log != nil {
		log.Err(fmt.Sprintf("%s: %v", manifest.CleanName(f.Filename), last))
	}
	return "failed", 0, last
}

func localMatches(path string, f manifest.File) bool {
	if len(f.SHAContent) != 20 {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if f.Size > 0 && uint64(fi.Size()) != f.Size {
		return false
	}
	fp, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fp.Close()
	h := sha1.New()
	buf := make([]byte, 1024*1024)
	for {
		n, err := fp.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	sum := h.Sum(nil)
	for i := 0; i < 20; i++ {
		if sum[i] != f.SHAContent[i] {
			return false
		}
	}
	return true
}
