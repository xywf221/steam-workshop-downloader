package services

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/download"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
)

// DownloadJob is one queued/running/finished item.
type DownloadJob struct {
	ID          int64  `json:"id"`
	AppID       int    `json:"app_id"`
	WorkshopID  uint64 `json:"workshop_id"`
	Title       string `json:"title"`
	Status      string `json:"status"` // queued|running|done|failed
	FilesTotal  int    `json:"files_total"`
	FilesDone   int    `json:"files_done"`
	BytesDone   int64  `json:"bytes_done"`
	BytesHuman  string `json:"bytes_human"`
	Percent     int    `json:"percent"`
	Message     string `json:"message"`
	Error       string `json:"error,omitempty"`
	OutDir      string `json:"out_dir"`
}

// DownloadEvent is emitted on progress ("download:progress").
type DownloadEvent struct {
	Job DownloadJob `json:"job"`
}

// DownloadService manages the download queue and emits progress events.
type DownloadService struct {
	sess *SessionManager

	mu     sync.Mutex
	jobs   []*DownloadJob
	nextID int64
	queue  chan *DownloadJob
	once   sync.Once

	// emitFn is set by main after the Wails app exists (app.Event.Emit).
	emitFn func(event string, data any)
}

// NewDownloadService builds the service. emitFn may be nil (events dropped).
func NewDownloadService(sess *SessionManager, emitFn func(string, any)) *DownloadService {
	return &DownloadService{
		sess:   sess,
		queue:  make(chan *DownloadJob, 256),
		emitFn: emitFn,
	}
}

// SetEmitter wires the Wails event emitter after app creation.
func (s *DownloadService) SetEmitter(fn func(string, any)) { s.emitFn = fn }

func (s *DownloadService) emit(job *DownloadJob) {
	if s.emitFn != nil {
		s.emitFn("download:progress", DownloadEvent{Job: *job})
	}
}

// Enqueue queues one item (collections expand into children) and returns the job(s).
func (s *DownloadService) Enqueue(appID int, workshopID uint64, title string) ([]DownloadJob, error) {
	if appID <= 0 || workshopID == 0 {
		return nil, fmt.Errorf("appid and workshopid required")
	}
	s.once.Do(func() { go s.worker() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return nil, fmt.Errorf("steam session: %w", err)
	}
	ids, err := workshop.ResolveIDs(ctx, sess.CM, []uint64{workshopID}, nil)
	if err != nil || len(ids) == 0 {
		ids = []uint64{workshopID}
	}

	out := make([]DownloadJob, 0, len(ids))
	s.mu.Lock()
	for _, id := range ids {
		s.nextID++
		j := &DownloadJob{
			ID:         s.nextID,
			AppID:      appID,
			WorkshopID: id,
			Title:      title,
			Status:     "queued",
			OutDir:     s.sess.OutputDir(),
		}
		if j.Title == "" || len(ids) > 1 {
			j.Title = fmt.Sprintf("%d", id)
		}
		s.jobs = append(s.jobs, j)
		s.queue <- j
		out = append(out, *j)
	}
	s.mu.Unlock()
	for i := range out {
		s.emit(&out[i])
	}
	return out, nil
}

// List returns all jobs (newest last).
func (s *DownloadService) List() []DownloadJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DownloadJob, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = *j
	}
	return out
}

// Clear removes finished/failed jobs.
func (s *DownloadService) Clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.jobs[:0]
	var removed int
	for _, j := range s.jobs {
		if j.Status == "done" || j.Status == "failed" {
			removed++
			continue
		}
		kept = append(kept, j)
	}
	s.jobs = kept
	return removed
}

func (s *DownloadService) worker() {
	for job := range s.queue {
		s.runJob(job)
	}
}

func (s *DownloadService) runJob(job *DownloadJob) {
	s.update(job, func(j *DownloadJob) {
		j.Status = "running"
		j.Message = "connecting"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cdnClient, err := s.sess.CDN(ctx)
	if err != nil {
		s.fail(job, fmt.Errorf("steam session: %w", err))
		return
	}

	prog := &jobProgress{svc: s, job: job}
	stats := download.DownloadItem(ctx, cdnClient, job.WorkshopID, s.sess.OutputDir(), nil, download.Options{
		Retries:  5,
		Progress: prog,
	})

	s.update(job, func(j *DownloadJob) {
		j.BytesDone = stats.BytesDone
		j.BytesHuman = formatBytes(stats.BytesDone)
		if stats.FullyFailed() {
			j.Status = "failed"
			j.Error = "download failed"
			j.Message = ""
		} else {
			j.Status = "done"
			j.Percent = 100
			j.Message = fmt.Sprintf("%d files", stats.OK+stats.Skipped)
			j.OutDir = stats.OutDir
			if stats.Name != "" {
				j.Title = stats.Name
			}
		}
	})
}

func (s *DownloadService) update(job *DownloadJob, fn func(*DownloadJob)) {
	s.mu.Lock()
	fn(job)
	snapshot := *job
	s.mu.Unlock()
	s.emit(&snapshot)
}

func (s *DownloadService) fail(job *DownloadJob, err error) {
	s.update(job, func(j *DownloadJob) {
		j.Status = "failed"
		j.Error = err.Error()
		j.Message = ""
	})
}

// jobProgress adapts download.Progress to job updates + events.
type jobProgress struct {
	svc *DownloadService
	job *DownloadJob
	total int64
	done  int64
}

func (p *jobProgress) StartFiles(n int) {
	atomic.StoreInt64(&p.total, int64(n))
	p.svc.update(p.job, func(j *DownloadJob) {
		j.FilesTotal = n
		j.Message = "downloading"
	})
}
func (p *jobProgress) FileOK(filename string, sizeBytes int64)   { p.fileDone(sizeBytes) }
func (p *jobProgress) FileSkip(filename string, sizeBytes int64) { p.fileDone(sizeBytes) }
func (p *jobProgress) FileFail(filename string, err error)       { p.fileDone(0) }
func (p *jobProgress) Retry(filename string, attempt, retries int, err error, backoffS int) {
	p.svc.update(p.job, func(j *DownloadJob) {
		j.Message = fmt.Sprintf("retry %s (%d/%d)", filename, attempt, retries)
	})
}
func (p *jobProgress) EndFiles(stats download.Stats) {}

func (p *jobProgress) fileDone(size int64) {
	done := atomic.AddInt64(&p.done, 1)
	total := atomic.LoadInt64(&p.total)
	p.svc.update(p.job, func(j *DownloadJob) {
		j.FilesDone = int(done)
		j.BytesDone += size
		j.BytesHuman = formatBytes(j.BytesDone)
		if total > 0 {
			j.Percent = int(done * 100 / total)
		}
	})
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
