package daemon

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

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

// writeState refreshes the state file with the current pid, version, and
// scheduler-reported next scan time. StartedAt is set on first write only.
func (r *Runner) writeState() error {
	if r.state == nil {
		return nil
	}
	return r.state.Update(func(s *State) {
		s.Version = r.version
		s.PID = os.Getpid()
		if s.StartedAt.IsZero() {
			s.StartedAt = time.Now().UTC()
		}
		if r.sched != nil {
			s.NextScanAt = r.sched.Next()
		}
	})
}
