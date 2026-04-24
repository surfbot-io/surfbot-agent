package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
)

// drainingScheduler runs until ctx is canceled, then waits `drain`
// duration before returning — simulating an in-flight scan that takes
// time to wind down after the shutdown signal.
type drainingScheduler struct {
	drain time.Duration
	mu    sync.Mutex
	ran   bool
}

func (d *drainingScheduler) Next() time.Time { return time.Time{} }
func (d *drainingScheduler) Run(ctx context.Context) error {
	d.mu.Lock()
	d.ran = true
	d.mu.Unlock()
	<-ctx.Done()
	time.Sleep(d.drain)
	return nil
}

// timestampingScheduler records the moment its Run context is canceled.
// Combined with httptest.Server handler timing, this lets tests assert
// the HTTP-before-scheduler shutdown ordering without racing on Stop.
type timestampingScheduler struct {
	ctxCanceledAt *atomic.Int64
}

func (t *timestampingScheduler) Next() time.Time { return time.Time{} }
func (t *timestampingScheduler) Run(ctx context.Context) error {
	<-ctx.Done()
	t.ctxCanceledAt.Store(time.Now().UnixNano())
	return nil
}

// TestShutdownUI_HTTPDrainsBeforeScheduler asserts the R4 sequencing:
// the HTTP server is drained before the scheduler is asked to stop.
// We observe the ordering by recording timestamps when the in-flight
// HTTP handler returns and when sched.Run observes its context cancel
// (which happens at runner.Stop, i.e. the step AFTER HTTP drain).
func TestShutdownUI_HTTPDrainsBeforeScheduler(t *testing.T) {
	var schedCtxCanceledAt atomic.Int64
	sched := &timestampingScheduler{
		ctxCanceledAt: &schedCtxCanceledAt,
	}
	dir := t.TempDir()
	stateStore := daemon.NewStateStore(filepath.Join(dir, "state.json"))
	logger := daemon.NewLogger(filepath.Join(dir, "test.log"), daemon.LoggerOptions{MaxSizeMB: 1})
	t.Cleanup(func() { _ = logger.Close() })

	runner := daemon.NewRunner(daemon.RunnerConfig{
		Scheduler: sched,
		State:     stateStore,
		Logger:    logger,
		Heartbeat: 50 * time.Millisecond,
		Version:   "test",
	})
	require.NoError(t, runner.Start())

	var handlerReturnedAt atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		handlerReturnedAt.Store(time.Now().UnixNano())
		w.WriteHeader(http.StatusOK)
	}))
	srv.Start()
	t.Cleanup(srv.Close)

	// Fire a long-running request and wait for it to reach the handler.
	reqDone := make(chan struct{})
	go func() {
		resp, err := http.Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		close(reqDone)
	}()
	time.Sleep(50 * time.Millisecond)

	shutdownUI(srv.Config, runner, 5*time.Second, nil)
	<-reqDone

	require.NotZero(t, handlerReturnedAt.Load(), "in-flight HTTP handler must have completed")
	require.NotZero(t, schedCtxCanceledAt.Load(), "scheduler must have observed ctx cancel")
	require.Greater(t, schedCtxCanceledAt.Load(), handlerReturnedAt.Load(),
		"scheduler ctx cancel must fire AFTER the HTTP handler returns (R4 ordering)")
}

// TestShutdownUI_SchedulerTimeoutIsClean asserts that a scheduler which
// refuses to drain within grace does not panic or hang — shutdownUI
// logs a warning and returns so the process can exit.
func TestShutdownUI_SchedulerTimeoutIsClean(t *testing.T) {
	// Scheduler that ignores ctx cancel for longer than the grace.
	sched := &drainingScheduler{drain: 2 * time.Second}
	dir := t.TempDir()
	stateStore := daemon.NewStateStore(filepath.Join(dir, "state.json"))
	logger := daemon.NewLogger(filepath.Join(dir, "test.log"), daemon.LoggerOptions{MaxSizeMB: 1})
	t.Cleanup(func() { _ = logger.Close() })

	runner := daemon.NewRunner(daemon.RunnerConfig{
		Scheduler: sched,
		State:     stateStore,
		Logger:    logger,
		Heartbeat: 50 * time.Millisecond,
		Version:   "test",
	})
	require.NoError(t, runner.Start())

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Start()
	t.Cleanup(srv.Close)

	start := time.Now()
	shutdownUI(srv.Config, runner, 100*time.Millisecond, nil)
	elapsed := time.Since(start)

	// We should return promptly when the scheduler misses its grace —
	// not hang until the full drain finishes.
	require.Less(t, elapsed, time.Second, "shutdownUI must return after scheduler grace, not wait for full drain")
}
