package web

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
)

// JobStatus is the lifecycle of one download job.
type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

// DownloadJob is one workshop item (or collection child) download.
type DownloadJob struct {
	ID          string
	AppID       int
	WorkshopID  uint64
	Title       string
	Status      JobStatus
	Message     string
	OutDir      string
	FilesTotal  int
	FilesDone   int
	FilesOK     int
	FilesFail   int
	FilesSkip   int
	BytesDone   int64
	CurrentFile string
	Error       string
	CreatedAt   float64
	StartedAt   *float64
	FinishedAt  *float64

	mu sync.Mutex
}

// ToDict is the JSON-friendly view.
func (j *DownloadJob) ToDict() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	pct := 0.0
	if j.FilesTotal > 0 {
		pct = 100.0 * float64(j.FilesDone) / float64(j.FilesTotal)
	} else if j.Status == JobDone {
		pct = 100.0
	}
	duration := 0.0
	if j.StartedAt != nil {
		end := float64(time.Now().UnixNano()) / 1e9
		if j.FinishedAt != nil {
			end = *j.FinishedAt
		}
		duration = end - *j.StartedAt
		if duration < 0 {
			duration = 0
		}
	}
	title := j.Title
	if title == "" {
		title = fmt.Sprintf("%d", j.WorkshopID)
	}
	bytesHuman := "0 B"
	if j.BytesDone > 0 {
		bytesHuman = util.FormatSize(j.BytesDone)
	}
	return map[string]any{
		"id":               j.ID,
		"app_id":           j.AppID,
		"workshop_id":      j.WorkshopID,
		"title":            title,
		"status":           string(j.Status),
		"message":          j.Message,
		"out_dir":          j.OutDir,
		"files_total":      j.FilesTotal,
		"files_done":       j.FilesDone,
		"files_ok":         j.FilesOK,
		"files_fail":       j.FilesFail,
		"files_skip":       j.FilesSkip,
		"bytes_done":       j.BytesDone,
		"bytes_done_human": bytesHuman,
		"percent":          round1(pct),
		"current_file":     j.CurrentFile,
		"error":            j.Error,
		"created_at":       j.CreatedAt,
		"started_at":       j.StartedAt,
		"finished_at":      j.FinishedAt,
		"duration_s":       round1(duration),
	}
}

type jobProgress struct {
	job *DownloadJob
	log *ui.Log
}

func (p *jobProgress) StartFiles(n int) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.FilesTotal = n
	p.job.FilesDone = 0
	p.job.Message = fmt.Sprintf("Downloading %d files", n)
}

func (p *jobProgress) FileOK(filename string, sizeBytes int64) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.FilesDone++
	p.job.FilesOK++
	p.job.BytesDone += sizeBytes
	p.job.CurrentFile = filename
	p.job.Message = "OK " + filename
}

func (p *jobProgress) FileSkip(filename string, sizeBytes int64) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.FilesDone++
	p.job.FilesSkip++
	p.job.CurrentFile = filename
	p.job.Message = "skip " + filename
}

func (p *jobProgress) Retry(filename string, attempt, retries int, err error, backoffS int) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.CurrentFile = filename
	p.job.Message = fmt.Sprintf("retry %d/%d %s: %v", attempt, retries, filename, err)
	if p.log != nil {
		p.log.Retry(fmt.Sprintf("%d/%d on %s: %v (backoff %ds)", attempt, retries, filename, err, backoffS))
	}
}

func (p *jobProgress) FileFail(filename string, err error) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.FilesDone++
	p.job.FilesFail++
	p.job.CurrentFile = filename
	msg := "FAIL " + filename
	if err != nil {
		msg = fmt.Sprintf("FAIL %s: %v", filename, err)
	}
	p.job.Message = msg
}

func (p *jobProgress) EndFiles(stats download.Stats) {
	p.job.mu.Lock()
	defer p.job.mu.Unlock()
	p.job.FilesOK = stats.OK
	p.job.FilesFail = stats.Fail
	p.job.FilesSkip = stats.Skipped
	p.job.BytesDone = stats.BytesDone
	if stats.OutDir != "" {
		p.job.OutDir = stats.OutDir
	}
	if stats.Name != "" {
		p.job.Title = stats.Name
	}
	p.job.CurrentFile = ""
}

// DownloadManager is a serial *item* queue; files inside an item may run in parallel.
type DownloadManager struct {
	steam     *SteamSession
	outputDir string
	log       *ui.Log
	retries   int
	fileJobs  int

	mu      sync.Mutex
	jobsMap map[string]*DownloadJob
	order   []string
	queue   chan string
	idSeq   atomic.Uint64
	closed  bool
}

// NewDownloadManager starts the worker loop.
// fileJobs: 0 = auto (cap 8), matching CLI -j.
func NewDownloadManager(steam *SteamSession, outputDir string, log *ui.Log, retries, fileJobs int) *DownloadManager {
	if retries < 1 {
		retries = 5
	}
	m := &DownloadManager{
		steam:     steam,
		outputDir: outputDir,
		log:       log,
		retries:   retries,
		fileJobs:  fileJobs,
		jobsMap:   make(map[string]*DownloadJob),
		queue:     make(chan string, 256),
	}
	go m.loop()
	return m
}

// Enqueue queues one workshop id (expands collections into children).
func (m *DownloadManager) Enqueue(appID int, workshopID uint64, title string) []*DownloadJob {
	var ids []uint64
	val, err := m.steam.Call(workerWait, func(ctx context.Context, sess *steam.Session) (any, error) {
		return workshop.ResolveIDs(ctx, sess.CM, []uint64{workshopID}, m.log)
	})
	if err != nil {
		if m.log != nil {
			m.log.Err(fmt.Sprintf("resolve_ids %d: %v", workshopID, err))
		}
		ids = []uint64{workshopID}
	} else {
		ids, _ = val.([]uint64)
		if len(ids) == 0 {
			ids = []uint64{workshopID}
		}
	}

	created := make([]*DownloadJob, 0, len(ids))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return created
	}
	for _, wid := range ids {
		if existing := m.findActiveLocked(appID, wid); existing != nil {
			created = append(created, existing)
			continue
		}
		id := fmt.Sprintf("dl-%d", m.idSeq.Add(1))
		t := title
		if len(ids) > 1 && t == "" {
			t = fmt.Sprintf("%d", wid)
		}
		job := &DownloadJob{
			ID:         id,
			AppID:      appID,
			WorkshopID: wid,
			Title:      t,
			Status:     JobQueued,
			Message:    "Queued",
			OutDir:     filepath.Join(m.outputDir, fmt.Sprintf("%d", wid)),
			CreatedAt:  float64(time.Now().UnixNano()) / 1e9,
		}
		m.jobsMap[id] = job
		m.order = append(m.order, id)
		// Bound history so long-running servers do not grow forever.
		const maxHistory = 500
		if len(m.order) > maxHistory {
			drop := m.order[:len(m.order)-maxHistory]
			m.order = m.order[len(m.order)-maxHistory:]
			for _, oldID := range drop {
				if old := m.jobsMap[oldID]; old != nil && old.Status != JobQueued && old.Status != JobRunning {
					delete(m.jobsMap, oldID)
				}
			}
		}
		select {
		case m.queue <- id:
		default:
			// Channel full — still try non-blocking via goroutine so we don't
			// hold m.mu while the download worker is idle waiting on us.
			go func(jid string) {
				m.mu.Lock()
				closed := m.closed
				m.mu.Unlock()
				if closed {
					return
				}
				select {
				case m.queue <- jid:
				case <-time.After(30 * time.Second):
					if m.log != nil {
						m.log.Err(fmt.Sprintf("[DL] drop queue push %s (timeout)", jid))
					}
				}
			}(id)
		}
		created = append(created, job)
		if m.log != nil {
			m.log.Info(fmt.Sprintf("[DL] queued %d -> %s", wid, job.OutDir))
		}
	}
	return created
}

func (m *DownloadManager) findActiveLocked(appID int, workshopID uint64) *DownloadJob {
	for i := len(m.order) - 1; i >= 0; i-- {
		job := m.jobsMap[m.order[i]]
		if job == nil {
			continue
		}
		if job.AppID == appID && job.WorkshopID == workshopID &&
			(job.Status == JobQueued || job.Status == JobRunning) {
			return job
		}
	}
	return nil
}

// ListJobs returns newest-first jobs up to limit.
func (m *DownloadManager) ListJobs(limit int) []*DownloadJob {
	if limit < 1 {
		limit = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*DownloadJob, 0, limit)
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		if j := m.jobsMap[m.order[i]]; j != nil {
			out = append(out, j)
		}
	}
	return out
}

// GetJob looks up by id.
func (m *DownloadManager) GetJob(id string) *DownloadJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobsMap[id]
}

// Counts returns status tallies.
func (m *DownloadManager) Counts() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := map[string]int{
		"queued": 0, "running": 0, "done": 0, "failed": 0,
		"total": len(m.jobsMap),
	}
	for _, j := range m.jobsMap {
		j.mu.Lock()
		st := j.Status
		j.mu.Unlock()
		c[string(st)]++
	}
	return c
}

// Close stops accepting new work (in-flight may finish).
func (m *DownloadManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	close(m.queue)
}

func (m *DownloadManager) loop() {
	for id := range m.queue {
		m.mu.Lock()
		job := m.jobsMap[id]
		m.mu.Unlock()
		if job == nil {
			continue
		}
		m.runOne(job)
	}
}

func (m *DownloadManager) runOne(job *DownloadJob) {
	now := float64(time.Now().UnixNano()) / 1e9
	job.mu.Lock()
	job.Status = JobRunning
	job.StartedAt = &now
	job.Message = "Starting"
	job.Error = ""
	title := job.Title
	wid := job.WorkshopID
	job.mu.Unlock()

	if m.log != nil {
		if title == "" {
			title = "…"
		}
		m.log.Stage("DL", fmt.Sprintf("Downloading %d (%s)", wid, title))
	}

	prog := &jobProgress{job: job, log: m.log}
	val, err := m.steam.CallWithCDN(0, func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error) {
		if err := cdnClient.EnsureServers(ctx); err != nil {
			return nil, err
		}
		// First-party CDN client is safe for concurrent ReadFile; Progress is mutexed.
		stats := download.DownloadItem(ctx, cdnClient, wid, m.outputDir, m.log, download.Options{
			Retries:  m.retries,
			Jobs:     m.fileJobs, // 0 = auto
			Progress: prog,
		})
		return stats, nil
	})

	fin := float64(time.Now().UnixNano()) / 1e9
	job.mu.Lock()
	defer job.mu.Unlock()
	job.FinishedAt = &fin
	if err != nil {
		job.Status = JobFailed
		job.Error = err.Error()
		job.Message = "Failed: " + err.Error()
		if m.log != nil {
			m.log.Err(fmt.Sprintf("[DL] %d: %v", wid, err))
		}
		return
	}
	stats, _ := val.(download.Stats)
	if stats.FullyFailed() {
		job.Status = JobFailed
		if job.Error == "" {
			job.Error = "download failed"
		}
		job.Message = "Failed"
		if m.log != nil {
			m.log.Err(fmt.Sprintf("[DL] %d failed", wid))
		}
		return
	}
	job.Status = JobDone
	job.Message = fmt.Sprintf("Done — %d ok / %d skip / %d fail", job.FilesOK, job.FilesSkip, job.FilesFail)
	if m.log != nil {
		m.log.OK(fmt.Sprintf("[DL] %d -> %s", wid, job.OutDir))
	}
}
