package intervalsched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Config controls the IntervalScheduler. All durations must be ≥ 1 minute
// in production; tests bypass Validate to drive the scheduler with much
// shorter intervals.
type Config struct {
	FullInterval  time.Duration
	QuickInterval time.Duration
	Jitter        time.Duration
	Window        MaintenanceWindow
	QuickTools    []string
	RunOnStart    bool
}

// Validate enforces the rules from spec §2. Returns a soft warning string
// (non-fatal) when quick_check_interval ≥ full_scan_interval — the caller
// logs it and the scheduler disables quick checks for the session.
func (c *Config) Validate() (warning string, err error) {
	if c.FullInterval < time.Minute {
		return "", fmt.Errorf("full_scan_interval must be >= 1m, got %s", c.FullInterval)
	}
	if c.QuickInterval > 0 && c.QuickInterval < time.Minute {
		return "", fmt.Errorf("quick_check_interval must be >= 1m, got %s", c.QuickInterval)
	}
	if c.QuickInterval > 0 && c.QuickInterval >= c.FullInterval {
		warning = "quick_check_interval >= full_scan_interval; disabling quick checks"
		c.QuickInterval = 0
	}
	if c.Window.Enabled {
		startMins := c.Window.Start.Hour*60 + c.Window.Start.Minute
		endMins := c.Window.End.Hour*60 + c.Window.End.Minute
		if startMins == endMins {
			return warning, errors.New("maintenance_window start and end must differ")
		}
	}
	// Cap jitter so it never dominates short intervals (open question 13.1).
	if c.Jitter > 0 {
		minInterval := c.FullInterval
		if c.QuickInterval > 0 && c.QuickInterval < minInterval {
			minInterval = c.QuickInterval
		}
		if c.Jitter > minInterval/10 {
			c.Jitter = minInterval / 10
		}
	}
	return warning, nil
}

// ScanResult is the structured outcome of one scheduled scan, fed into
// the onTick callback so the outer Runner can mirror it into
// daemon.state.json.
type ScanResult struct {
	Profile   Profile
	StartedAt time.Time
	Duration  time.Duration
	Error     string
}

// ScanRunner is the boundary between the scheduler and the rest of
// surfbot. The production implementation lives in the daemon package and
// invokes internal/pipeline. Tests inject a fake.
type ScanRunner interface {
	Run(ctx context.Context, profile Profile) error
}

// IntervalScheduler implements daemon.Scheduler. It runs full and quick
// scans on independent cadences, persists cursors across restarts, and
// honors a maintenance window.
type IntervalScheduler struct {
	cfg     Config
	clock   Clock
	store   *ScheduleStateStore
	scanner ScanRunner
	logger  *slog.Logger
	onTick  func(ScanResult)
	rng     *rand.Rand

	mu    sync.Mutex
	state ScheduleState
}

// Options bundles non-config dependencies for New.
type Options struct {
	Clock      Clock
	StateStore *ScheduleStateStore
	Scanner    ScanRunner
	Logger     *slog.Logger
	OnTick     func(ScanResult)
	RandSeed   int64 // 0 → time.Now().UnixNano()
}

// New constructs an IntervalScheduler. Validate the Config before calling
// — New does not re-validate.
func New(cfg Config, opts Options) *IntervalScheduler {
	if opts.Clock == nil {
		opts.Clock = NewRealClock()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	seed := opts.RandSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &IntervalScheduler{
		cfg:     cfg,
		clock:   opts.Clock,
		store:   opts.StateStore,
		scanner: opts.Scanner,
		logger:  opts.Logger,
		onTick:  opts.OnTick,
		rng:     rand.New(rand.NewSource(seed)),
	}
}

// State returns a snapshot of the current schedule cursors. Safe for
// concurrent callers (status display, tests).
func (s *IntervalScheduler) State() ScheduleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Next satisfies the daemon.Scheduler interface. Returns the earliest of
// the next full or quick tick, adjusted for maintenance window.
func (s *IntervalScheduler) Next() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	next := s.computeNext(now)
	if s.cfg.Window.Contains(next) {
		next = s.cfg.Window.NextOpen(next)
	}
	return next
}

// computeNext returns the earliest of the two configured ticks, taking
// the persisted cursors into account. Caller holds s.mu.
func (s *IntervalScheduler) computeNext(now time.Time) time.Time {
	var nextFull, nextQuick time.Time
	if s.cfg.FullInterval > 0 {
		base := s.state.LastFullAt
		if base.IsZero() {
			base = now
		}
		nextFull = base.Add(s.cfg.FullInterval)
	}
	if s.cfg.QuickInterval > 0 {
		base := s.state.LastQuickAt
		if base.IsZero() {
			base = now
		}
		nextQuick = base.Add(s.cfg.QuickInterval)
	}
	switch {
	case nextFull.IsZero():
		return nextQuick
	case nextQuick.IsZero():
		return nextFull
	case nextQuick.Before(nextFull):
		return nextQuick
	default:
		return nextFull
	}
}

// Run blocks until ctx is canceled, triggering scans on schedule. It
// implements the loop described in spec §3.3.
func (s *IntervalScheduler) Run(ctx context.Context) error {
	if s.store != nil {
		st, err := s.store.Load()
		if err != nil {
			s.logger.Warn("schedule state load failed; starting fresh", "err", err)
		} else {
			s.mu.Lock()
			s.state = st
			s.mu.Unlock()
		}
	}

	if s.cfg.RunOnStart {
		now := s.clock.Now()
		if !s.cfg.Window.Contains(now) {
			s.runScan(ctx, ProfileFull)
		} else {
			s.logger.Info("scheduler.skip", "reason", "run_on_start_in_window",
				"resume", s.cfg.Window.NextOpen(now))
		}
	}

	for {
		now := s.clock.Now()
		profile, target := s.nextTarget(now)
		if target.IsZero() {
			// No intervals configured — block until cancellation so the
			// outer Runner does not exit.
			<-ctx.Done()
			return nil
		}
		if s.cfg.Window.Contains(target) {
			open := s.cfg.Window.NextOpen(target)
			s.logger.Info("scheduler.skip",
				"reason", "maintenance_window",
				"profile", string(profile),
				"resume", open)
			target = open
		}

		wait := target.Sub(now)
		if wait < 0 {
			wait = 0
		}
		// Apply jitter as a non-negative addition; subtracting could push
		// us into the past on a fresh start.
		if s.cfg.Jitter > 0 {
			wait += time.Duration(s.rng.Int63n(int64(s.cfg.Jitter) + 1))
		}

		s.logger.Info("scheduler.tick",
			"profile", string(profile),
			"in", wait,
			"at", now.Add(wait))

		timer := s.clock.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C():
		}

		if s.cfg.Window.Contains(s.clock.Now()) {
			// Clock skew or DST shift pushed us into the window; retry.
			continue
		}
		s.runScan(ctx, profile)
	}
}

// nextTarget returns the profile that should fire next and the absolute
// target time, BEFORE any jitter or window adjustment.
func (s *IntervalScheduler) nextTarget(now time.Time) (Profile, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var fullAt, quickAt time.Time
	if s.cfg.FullInterval > 0 {
		base := s.state.LastFullAt
		if base.IsZero() {
			base = now
		}
		fullAt = base.Add(s.cfg.FullInterval)
	}
	if s.cfg.QuickInterval > 0 {
		base := s.state.LastQuickAt
		if base.IsZero() {
			base = now
		}
		quickAt = base.Add(s.cfg.QuickInterval)
	}

	switch {
	case fullAt.IsZero() && quickAt.IsZero():
		return "", time.Time{}
	case fullAt.IsZero():
		return ProfileQuick, quickAt
	case quickAt.IsZero():
		return ProfileFull, fullAt
	case quickAt.Before(fullAt):
		return ProfileQuick, quickAt
	default:
		return ProfileFull, fullAt
	}
}

// runScan invokes the scan runner, updates the cursor, persists state,
// and emits the onTick callback. A failed scan still advances the cursor
// so a permanent failure does not cause a tight retry loop.
func (s *IntervalScheduler) runScan(ctx context.Context, profile Profile) {
	started := s.clock.Now()
	s.logger.Info("scheduler.scan_start", "profile", string(profile))

	var runErr error
	if s.scanner != nil {
		runErr = s.scanner.Run(ctx, profile)
	}
	duration := s.clock.Now().Sub(started)

	s.mu.Lock()
	finished := s.clock.Now()
	status := "ok"
	errStr := ""
	if runErr != nil {
		status = "failed"
		errStr = runErr.Error()
	}
	switch profile {
	case ProfileFull:
		s.state.LastFullAt = finished
		s.state.LastFullStatus = status
		s.state.LastFullError = errStr
		if s.cfg.FullInterval > 0 {
			s.state.NextFullAt = finished.Add(s.cfg.FullInterval)
		}
	case ProfileQuick:
		s.state.LastQuickAt = finished
		s.state.LastQuickStatus = status
		s.state.LastQuickError = errStr
		if s.cfg.QuickInterval > 0 {
			s.state.NextQuickAt = finished.Add(s.cfg.QuickInterval)
		}
	}
	snapshot := s.state
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.Save(snapshot); err != nil {
			s.logger.Warn("schedule state save failed", "err", err)
		}
	}

	if runErr != nil {
		s.logger.Warn("scheduler.scan_fail",
			"profile", string(profile),
			"duration", duration,
			"err", runErr)
	} else {
		s.logger.Info("scheduler.scan_done",
			"profile", string(profile),
			"duration", duration)
	}

	if s.onTick != nil {
		s.onTick(ScanResult{
			Profile:   profile,
			StartedAt: started,
			Duration:  duration,
			Error:     errStr,
		})
	}
}
