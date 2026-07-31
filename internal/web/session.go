package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cdn"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// workerWait = UM budget (30s × 3) + slack, matching Python _WORKER_WAIT_S.
const workerWait = 30*time.Second*3 + 60*time.Second

type steamJob struct {
	fn      func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error)
	result  chan steamResult
	timeout time.Duration // 0 = no deadline on the worker side
	ctx     context.Context
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
	done    chan struct{} // closed when loop exits

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
		done:     make(chan struct{}),
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

// Call runs fn on the Steam worker. timeout=0 waits indefinitely for the result.
// The request context (if non-nil via CallCtx) cancels the wait; the worker still
// finishes the in-flight Steam call.
func (s *SteamSession) Call(timeout time.Duration, fn func(ctx context.Context, sess *steam.Session) (any, error)) (any, error) {
	return s.CallCtx(context.Background(), timeout, func(ctx context.Context, sess *steam.Session, _ *cdn.Client) (any, error) {
		return fn(ctx, sess)
	})
}

// CallWithCDN runs fn(sess, cdnClient) on the worker.
func (s *SteamSession) CallWithCDN(timeout time.Duration, fn func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error)) (any, error) {
	return s.CallCtx(context.Background(), timeout, fn)
}

// CallCtx is CallWithCDN with an external cancel for the waiter.
func (s *SteamSession) CallCtx(parent context.Context, timeout time.Duration, fn func(ctx context.Context, sess *steam.Session, cdnClient *cdn.Client) (any, error)) (any, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("Steam worker [%s] closed", s.name)
	}

	resCh := make(chan steamResult, 1)
	job := steamJob{fn: fn, result: resCh, timeout: timeout, ctx: parent}
	select {
	case s.jobs <- job:
	case <-parent.Done():
		return nil, parent.Err()
	case <-s.done:
		return nil, fmt.Errorf("Steam worker [%s] closed", s.name)
	}

	if timeout == 0 {
		select {
		case r := <-resCh:
			return r.val, r.err
		case <-parent.Done():
			return nil, parent.Err()
		case <-s.done:
			return nil, fmt.Errorf("Steam worker [%s] closed", s.name)
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-resCh:
		return r.val, r.err
	case <-timer.C:
		return nil, fmt.Errorf("Steam worker [%s] did not finish within %s (UM may be wedged; restart swd web)", s.name, timeout)
	case <-parent.Done():
		return nil, parent.Err()
	case <-s.done:
		return nil, fmt.Errorf("Steam worker [%s] closed", s.name)
	}
}

// HTTP returns the session HTTP client (proxy-aware after login).
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

// Close stops the worker and disconnects. Safe to call multiple times.
func (s *SteamSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.jobs)
	<-s.done
}

func (s *SteamSession) loop() {
	defer close(s.done)
	close(s.started)
	for job := range s.jobs {
		s.runJob(job)
	}
	s.mu.Lock()
	if s.sess != nil {
		s.sess.Close()
		s.sess = nil
		s.cdn = nil
	}
	s.mu.Unlock()
}

func (s *SteamSession) runJob(job steamJob) {
	parent := job.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx := parent
	var cancel context.CancelFunc
	if job.timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, job.timeout)
		defer cancel()
	}

	sess, cdnClient, err := s.ensure(ctx)
	if err != nil {
		select {
		case job.result <- steamResult{err: err}:
		default:
		}
		return
	}
	val, err := job.fn(ctx, sess, cdnClient)
	select {
	case job.result <- steamResult{val: val, err: err}:
	default:
		// waiter already timed out / cancelled — drop
	}
}

func (s *SteamSession) ensure(ctx context.Context) (*steam.Session, *cdn.Client, error) {
	// Only called from the single worker goroutine — no concurrent ensure.
	s.mu.Lock()
	if s.sess != nil && s.sess.CM != nil && s.sess.CM.LoggedOn() {
		sess, c := s.sess, s.cdn
		s.mu.Unlock()
		return sess, c, nil
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
		return nil, nil, fmt.Errorf("Steam anonymous login failed [%s]: %w", s.name, err)
	}
	cdnClient := cdn.New(sess.CM, sess.HTTP(), s.log)

	s.mu.Lock()
	s.sess = sess
	s.cdn = cdnClient
	s.mu.Unlock()
	return sess, cdnClient, nil
}
