package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// worker wait = UM budget + slack (matches Python _WORKER_WAIT_S).
const workerWait = 30*time.Second*3 + 60*time.Second

type steamJob struct {
	fn      func(ctx context.Context, sess *steam.Session) (any, error)
	result  chan steamResult
	timeout time.Duration // 0 = wait forever
}

type steamResult struct {
	val any
	err error
}

// SteamSession owns one anonymous CM session on a dedicated worker goroutine.
// Use separate instances for browse vs downloads so long CDN work never blocks QueryFiles.
type SteamSession struct {
	proxyURL string
	log      *ui.Log
	name     string

	jobs    chan steamJob
	started chan struct{}

	mu     sync.Mutex
	sess   *steam.Session
	cdn    *cdn.Client
	closed bool
}

// NewSteamSession starts the worker goroutine.
func NewSteamSession(proxyURL string, log *ui.Log, name string) *SteamSession {
	if name == "" {
		name = "steam"
	}
	s := &SteamSession{
		proxyURL: proxyURL,
		log:      log,
		name:     name,
		jobs:     make(chan steamJob, 64),
		started:  make(chan struct{}),
	}
	go s.loop()
	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
	}
	return s
}

// HasClient reports whether a session object exists (may still be dead).
func (s *SteamSession) HasClient() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess != nil && s.sess.CM != nil
}

// LoggedOn reports whether the CM is logged on.
func (s *SteamSession) LoggedOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == nil || s.sess.CM == nil {
		return false
	}
	return s.sess.CM.LoggedOn()
}

// Call runs fn on the Steam worker. timeout=0 waits indefinitely.
func (s *SteamSession) Call(timeout time.Duration, fn func(ctx context.Context, sess *steam.Session) (any, error)) (any, error) {
	resCh := make(chan steamResult, 1)
	job := steamJob{fn: fn, result: resCh, timeout: timeout}
	select {
	case s.jobs <- job:
	default:
		// still try; may block if queue full
		s.jobs <- job
	}
	if timeout == 0 {
		r := <-resCh
		return r.val, r.err
	}
	select {
	case r := <-resCh:
		return r.val, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("Steam worker [%s] did not finish within %s (UM may be wedged; restart swd web)", s.name, timeout)
	}
}

// CallWithCDN runs fn(sess, cdnClient) on the worker.
func (s *SteamSession) CallWithCDN(timeout time.Duration, fn func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error)) (any, error) {
	return s.Call(timeout, func(ctx context.Context, sess *steam.Session) (any, error) {
		s.mu.Lock()
		c := s.cdn
		s.mu.Unlock()
		if c == nil {
			return nil, fmt.Errorf("CDN client not ready")
		}
		return fn(ctx, sess, c)
	})
}

// CM returns the live CM client (must only be used from inside Call).
func (s *SteamSession) CM() *cm.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == nil {
		return nil
	}
	return s.sess.CM
}

// HTTP returns the session HTTP client.
func (s *SteamSession) HTTP() *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == nil {
		return http.DefaultClient
	}
	return s.sess.HTTP()
}

// Warmup eagerly logs in so the first browser hit is not paying CM connect cost.
func (s *SteamSession) Warmup() {
	t0 := time.Now()
	_, err := s.Call(workerWait, func(ctx context.Context, sess *steam.Session) (any, error) {
		return sess, nil
	})
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("Steam warmup failed [%s]: %v", s.name, err))
		}
		return
	}
	if s.log != nil {
		s.log.OK(fmt.Sprintf("Steam ready [%s] (%.1fs)", s.name, time.Since(t0).Seconds()))
	}
}

// Close stops the worker and disconnects.
func (s *SteamSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.jobs)
}

func (s *SteamSession) loop() {
	close(s.started)
	for job := range s.jobs {
		ctx := context.Background()
		if job.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, job.timeout)
			// cancel after fn returns
			func() {
				defer cancel()
				sess, err := s.ensure(ctx)
				if err != nil {
					job.result <- steamResult{err: err}
					return
				}
				val, err := job.fn(ctx, sess)
				job.result <- steamResult{val: val, err: err}
			}()
			continue
		}
		sess, err := s.ensure(ctx)
		if err != nil {
			job.result <- steamResult{err: err}
			continue
		}
		val, err := job.fn(ctx, sess)
		job.result <- steamResult{val: val, err: err}
	}
	s.mu.Lock()
	if s.sess != nil {
		s.sess.Close()
		s.sess = nil
		s.cdn = nil
	}
	s.mu.Unlock()
}

func (s *SteamSession) ensure(ctx context.Context) (*steam.Session, error) {
	s.mu.Lock()
	if s.sess != nil && s.sess.CM != nil && s.sess.CM.LoggedOn() {
		sess := s.sess
		s.mu.Unlock()
		return sess, nil
	}
	if s.sess != nil {
		if s.log != nil {
			s.log.Warn(fmt.Sprintf("Steam session [%s] dead; re-login on worker", s.name))
		}
		s.sess.Close()
		s.sess = nil
		s.cdn = nil
	}
	s.mu.Unlock()

	sess, err := steam.InitSession(ctx, s.proxyURL, s.log)
	if err != nil {
		return nil, fmt.Errorf("Steam anonymous login failed [%s]: %w", s.name, err)
	}
	cdnClient := cdn.New(sess.CM, sess.HTTP(), s.log)

	s.mu.Lock()
	s.sess = sess
	s.cdn = cdnClient
	s.mu.Unlock()
	return sess, nil
}
