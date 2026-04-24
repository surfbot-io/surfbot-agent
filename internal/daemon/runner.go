package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// SchedulerPanicExit is the hook the scheduler supervisor calls to
// terminate the process after a scheduler panic. In production it is
// os.Exit so a crash-exit is unmissable in logs and the OS service
// manager will surface the non-zero status. Tests override it so the
// supervisor can be exercised without killing the test binary.
var SchedulerPanicExit = os.Exit

// SchedulerPanicGrace is how long the supervisor sleeps between the
// onPanic callback (typically a context cancel that shuts down the HTTP
// server) and the process exit. 2 seconds is enough for slog handlers to
// flush and for graceful shutdown to start draining in-flight requests.
// Overridable for tests.
var SchedulerPanicGrace = 2 * time.Second

// ErrShutdownTimeout is returned when Stop's grace period elapses before
// the runner goroutine exits. The service layer logs a warning and lets
// the OS service manager finish the kill.
var ErrShutdownTimeout = errors.New("daemon: runner shutdown timeout")

// Runner owns the daemon's background goroutine and the heartbeat ticker
// that keeps the state file fresh. It is intentionally small: scheduling
// logic lives behind the Scheduler interface so SPEC-X2 can swap it.
type Runner struct {
	sched     Scheduler
	state     *StateStore
	logger    *Logger
	heartbeat time.Duration
	version   string
	onPanic   func()
	started   time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// RunnerConfig bundles the runner's dependencies.
type RunnerConfig struct {
	Scheduler Scheduler
	State     *StateStore
	Logger    *Logger
	Heartbeat time.Duration // state-file refresh cadence (default 30s)
	Version   string        // surfbot version string written to state

	// OnSchedulerPanic fires synchronously from the scheduler goroutine
	// after a panic has been logged and the state file has been updated
	// with last_error. Typical use is to cancel the caller's top-level
	// context so auxiliary services (e.g. the HTTP server in
	// `surfbot ui`) start their own shutdown before the supervisor
	// terminates the process via SchedulerPanicExit.
	OnSchedulerPanic func()
}

// NewRunner constructs a runner. It does not start the goroutine.
func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 30 * time.Second
	}
	return &Runner{
		sched:     cfg.Scheduler,
		state:     cfg.State,
		logger:    cfg.Logger,
		heartbeat: cfg.Heartbeat,
		version:   cfg.Version,
		onPanic:   cfg.OnSchedulerPanic,
	}
}

// Start launches the runner goroutine. It is non-blocking and safe to
// call exactly once per Runner.
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return errors.New("daemon: runner already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	r.started = time.Now().UTC()

	if err := r.writeState(); err != nil && r.logger != nil {
		r.logger.Slog().Warn("initial state write failed", "err", err)
	}

	go r.loop(ctx)
	return nil
}

// Stop cancels the runner context and waits up to grace for the goroutine
// to exit. Returns ErrShutdownTimeout on timeout.
func (r *Runner) Stop(grace time.Duration) error {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(grace):
		return ErrShutdownTimeout
	}
}

// loop runs until ctx is canceled. It writes a state heartbeat on a
// ticker and concurrently lets the scheduler do its thing.
func (r *Runner) loop(ctx context.Context) {
	defer close(r.done)

	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		defer r.recoverSchedulerPanic()
		if err := r.sched.Run(ctx); err != nil && r.logger != nil {
			r.logger.Slog().Error("scheduler exited with error", "err", err)
		}
	}()

	ticker := time.NewTicker(r.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final state write before exit.
			_ = r.writeState()
			<-schedDone
			return
		case <-ticker.C:
			if err := r.writeState(); err != nil && r.logger != nil {
				r.logger.Slog().Warn("state heartbeat failed", "err", err)
			}
		}
	}
}

// recoverSchedulerPanic is deferred on the scheduler goroutine. It logs
// the panic with a full stack, records last_error in the state file,
// fires the caller's OnSchedulerPanic callback (typically a ctx cancel
// so the HTTP server begins its own shutdown), waits the configured
// grace for logs to flush, and finally terminates the process with
// SchedulerPanicExit(1). The re-panic path is deliberately not taken:
// a silent recovery would let a broken scheduler leave the process
// up, serving stale HTTP, with no scans firing — strictly worse than
// a clean non-zero exit.
func (r *Runner) recoverSchedulerPanic() {
	rec := recover()
	if rec == nil {
		return
	}
	stack := debug.Stack()
	if r.logger != nil {
		r.logger.Slog().Error("scheduler panicked",
			"panic", fmt.Sprint(rec),
			"stack", string(stack),
		)
	}
	if r.state != nil {
		_ = r.state.Update(func(s *State) {
			s.LastError = fmt.Sprintf("scheduler panic: %v", rec)
			s.WrittenAt = time.Now().UTC()
		})
	}
	if r.onPanic != nil {
		r.onPanic()
	}
	time.Sleep(SchedulerPanicGrace)
	SchedulerPanicExit(1)
}

// writeState refreshes the state file with the current pid, version, and
// scheduler-reported next scan time. StartedAt is set on first write only.
func (r *Runner) writeState() error {
	if r.state == nil {
		return nil
	}
	return r.state.Update(func(s *State) {
		s.Version = r.version
		s.PID = os.Getpid()
		now := time.Now().UTC()
		s.WrittenAt = now
		s.StartedAt = r.started
		if r.sched != nil {
			s.NextScanAt = r.sched.Next()
		}
	})
}
