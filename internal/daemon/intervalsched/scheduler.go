// Package intervalsched implements the surfbot daemon's master ticker for
// first-class schedules (SPEC-SCHED1.2b). The ticker periodically polls
// scan_schedules for due rows, claims a per-target lock, dispatches each
// to a bounded worker pool, and updates next_run_at via the RRULE
// expander. SCHED1.2a primitives (locks, worker pool, blackout evaluator,
// rrule expander, no-overlap validator, cascade recompute) compose into
// the loop below.
package intervalsched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// DefaultTickInterval is the master ticker poll cadence. SCHED1.3 will
// surface this as a configurable field on schedule_defaults; for 1.2b it
// is a hardcoded constant.
const DefaultTickInterval = 30 * time.Second

// listDueOverhead is the small extra count requested from ListDue beyond
// the number of free worker slots, so blackout-skipped rows do not
// starve real candidates within a tick.
const listDueOverhead = 5

// ScanRunner is the boundary between the master ticker and whatever
// actually executes a scan. SCHED1.2b ships internal/daemon.legacyScanRunner
// which discards EffectiveConfig.ToolConfig and invokes the existing
// pipeline orchestrator. SCHED1.2c will replace that with a runner that
// threads the resolved tool params through each detection tool.
type ScanRunner interface {
	// Run executes a scan for targetID using the resolved EffectiveConfig.
	// Returns the new scan ID for persistence on
	// scan_schedules.last_scan_id, or an error. The error is logged and
	// surfaced as ScheduleRunFailed.
	Run(ctx context.Context, scheduleID, targetID string, effective model.EffectiveConfig) (scanID string, err error)
}

// Dependencies bundles the stores + helpers the master ticker needs. The
// AdHocStore is unused in 1.2b but is wired in now so SCHED1.2c can plug
// pause-in-flight + ad-hoc dispatch without changing this signature.
type Dependencies struct {
	SchedStore    storage.ScheduleStore
	TmplStore     storage.TemplateStore
	BlackoutStore storage.BlackoutStore
	DefaultsStore storage.ScheduleDefaultsStore
	AdHocStore    storage.AdHocScanRunStore
	Runner        ScanRunner
	Log           *slog.Logger
	Clock         Clock
	TickInterval  time.Duration
	JitterSeed    int64
}

// inflightJob tracks one dispatched scan so the master ticker can cancel
// its ctx mid-flight when the target enters a new blackout window
// (SCHED1.2c R8). Keyed in Scheduler.inflight by jobKey().
type inflightJob struct {
	scheduleID string
	targetID   string
	acquiredAt time.Time
	cancel     context.CancelCauseFunc
}

// jobKey is the inflight map key. Schedule jobs use the schedule ID;
// SCHED1.2c will dispatch ad-hoc jobs with the "adhoc:" prefix so both
// kinds coexist without collision.
func jobKey(scheduleID string) string { return scheduleID }

// Scheduler is the master ticker. It owns the worker pool, target lock
// index, blackout evaluator, rrule expander, and the in-flight job
// table used by pause-in-flight.
type Scheduler struct {
	deps      Dependencies
	defaults  model.ScheduleDefaults
	pool      *WorkerPool
	locks     *TargetLockIndex
	blackouts *BlackoutEvaluator
	expander  *RRuleExpander

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	inflight sync.Map // map[string]*inflightJob, keyed by jobKey
}

// New wires the master ticker. It loads schedule_defaults synchronously
// (returning an error if the singleton row is missing — schema migration
// 0004 seeds a row, so this only happens against an empty DB) and primes
// the blackout evaluator's cache. Start must be called to begin the tick
// loop.
func New(deps Dependencies) (*Scheduler, error) {
	if deps.SchedStore == nil {
		return nil, fmt.Errorf("intervalsched.New: SchedStore is required")
	}
	if deps.TmplStore == nil {
		return nil, fmt.Errorf("intervalsched.New: TmplStore is required")
	}
	if deps.BlackoutStore == nil {
		return nil, fmt.Errorf("intervalsched.New: BlackoutStore is required")
	}
	if deps.DefaultsStore == nil {
		return nil, fmt.Errorf("intervalsched.New: DefaultsStore is required")
	}
	if deps.Runner == nil {
		return nil, fmt.Errorf("intervalsched.New: Runner is required")
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Clock == nil {
		deps.Clock = NewRealClock()
	}
	if deps.TickInterval <= 0 {
		deps.TickInterval = DefaultTickInterval
	}
	seed := deps.JitterSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	defaults, err := deps.DefaultsStore.Get(context.Background())
	if err != nil {
		return nil, fmt.Errorf("loading schedule_defaults: %w", err)
	}
	if defaults == nil {
		return nil, fmt.Errorf("schedule_defaults singleton row missing")
	}

	blackouts := NewBlackoutEvaluator(deps.BlackoutStore)
	if err := blackouts.Refresh(context.Background()); err != nil {
		// Non-fatal: the evaluator falls back to "no blackouts" while the
		// store is unavailable. Log and continue so the daemon still ticks.
		deps.Log.Warn("blackout evaluator initial refresh failed", "err", err)
	}

	maxConcurrent := defaults.MaxConcurrentScans
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	locks := NewTargetLockIndex()
	expander := NewRRuleExpander(*defaults, blackouts, deps.Clock, seed)

	s := &Scheduler{
		deps:      deps,
		defaults:  *defaults,
		locks:     locks,
		blackouts: blackouts,
		expander:  expander,
	}
	s.pool = NewWorkerPool(maxConcurrent, &scanJobRunner{s: s}, deps.Log)
	return s, nil
}

// Start launches the worker pool and the tick goroutine. Non-blocking.
// Calling Start twice on the same Scheduler is a no-op.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.ctx = runCtx
	s.cancel = cancel
	s.mu.Unlock()

	s.pool.Start(runCtx)
	s.wg.Add(1)
	go s.tickLoop()
	return nil
}

// Stop cancels the tick loop and waits for in-flight workers to drain
// (bounded by ctx). Safe to call multiple times.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.pool.Stop(ctx)
}

// Run satisfies the daemon.Scheduler interface used by daemon.Runner. It
// calls Start and blocks until ctx is canceled, then drains the pool with
// a fresh background context bounded by 30s so a normal SIGTERM does not
// terminate in-flight scans abruptly.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.Stop(stopCtx)
}

// Next satisfies the daemon.Scheduler interface. SCHED1.2b returns the
// nearest schedule's next_run_at (best-effort; ignores blackouts and
// errors). SCHED1.3 will surface a richer status.
func (s *Scheduler) Next() time.Time {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	due, err := s.deps.SchedStore.ListDue(ctx, s.deps.Clock.Now().Add(365*24*time.Hour), 1)
	if err != nil || len(due) == 0 {
		return time.Time{}
	}
	if due[0].NextRunAt == nil {
		return time.Time{}
	}
	return *due[0].NextRunAt
}

// tickLoop fires every TickInterval and dispatches due schedules.
func (s *Scheduler) tickLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.deps.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// tick performs one dispatch round.
func (s *Scheduler) tick(now time.Time) {
	if err := s.blackouts.Refresh(s.ctx); err != nil {
		s.deps.Log.Warn("blackout refresh failed; using stale cache", "err", err)
	}
	s.evaluateInFlightBlackouts(now)

	free := s.pool.Free()
	if free <= 0 {
		return
	}
	limit := free + listDueOverhead
	due, err := s.deps.SchedStore.ListDue(s.ctx, now, limit)
	if err != nil {
		s.deps.Log.Error("listing due schedules", "err", err)
		return
	}
	if len(due) == 0 {
		return
	}

	dispatched := 0
	for _, sched := range due {
		if dispatched >= free {
			break
		}
		if !s.locks.TryAcquire(sched.TargetID) {
			continue
		}
		active, _ := s.blackouts.IsActive(sched.TargetID, now)
		if active {
			s.locks.Release(sched.TargetID)
			s.handleBlackoutSkip(sched, now)
			continue
		}
		job := Job{
			ScheduleID: sched.ID,
			TargetID:   sched.TargetID,
			Payload:    sched,
		}
		if !s.pool.Dispatch(job) {
			s.locks.Release(sched.TargetID)
			break
		}
		dispatched++
	}
}

// handleBlackoutSkip records a skipped run and advances next_run_at past
// the active blackout. The lock is already released by the caller.
func (s *Scheduler) handleBlackoutSkip(sched model.Schedule, now time.Time) {
	if err := s.deps.SchedStore.RecordRun(s.ctx, sched.ID, model.ScheduleRunSkippedBlackout, nil, now); err != nil {
		s.deps.Log.Warn("record blackout-skip", "schedule_id", sched.ID, "err", err)
	}
	tmpl := s.loadTemplate(s.ctx, sched.TemplateID)
	next, err := s.expander.ComputeNextRunAt(sched, tmpl)
	if err != nil {
		s.deps.Log.Warn("compute next run after blackout skip",
			"schedule_id", sched.ID, "err", err)
		return
	}
	if err := s.deps.SchedStore.SetNextRunAt(s.ctx, sched.ID, next); err != nil {
		s.deps.Log.Warn("set next_run_at after blackout skip",
			"schedule_id", sched.ID, "err", err)
	}
}

// evaluateInFlightBlackouts is a no-op in SCHED1.2b. SCHED1.2c will
// iterate live jobs and cancel ctx for any whose target enters a new
// blackout window.
//
// TODO: SCHED1.2c — implement pause-in-flight ctx cancellation.
func (s *Scheduler) evaluateInFlightBlackouts(_ time.Time) {}

func (s *Scheduler) loadTemplate(ctx context.Context, id *string) *model.Template {
	if id == nil || *id == "" {
		return nil
	}
	tmpl, err := s.deps.TmplStore.Get(ctx, *id)
	if err != nil {
		s.deps.Log.Warn("load template", "template_id", *id, "err", err)
		return nil
	}
	return tmpl
}

// scanJobRunner adapts the worker pool's JobRunner contract to the
// scheduler's ScanRunner. It resolves the EffectiveConfig, invokes the
// runner, persists the run outcome, and recomputes next_run_at — all
// while holding the per-target lock.
type scanJobRunner struct {
	s *Scheduler
}

func (r *scanJobRunner) Run(ctx context.Context, job Job) error {
	defer r.s.locks.Release(job.TargetID)

	sched, ok := job.Payload.(model.Schedule)
	if !ok {
		return fmt.Errorf("scanJobRunner: unexpected payload type %T", job.Payload)
	}
	tmpl := r.s.loadTemplate(ctx, sched.TemplateID)
	effective, err := model.ResolveEffectiveConfig(sched, tmpl, r.s.defaults)
	if err != nil {
		_ = r.s.deps.SchedStore.RecordRun(ctx, sched.ID, model.ScheduleRunFailed, nil, time.Now().UTC())
		return fmt.Errorf("resolve effective config: %w", err)
	}

	jobCtx, cancel := context.WithCancelCause(ctx)
	key := jobKey(sched.ID)
	r.s.inflight.Store(key, &inflightJob{
		scheduleID: sched.ID,
		targetID:   sched.TargetID,
		acquiredAt: r.s.deps.Clock.Now(),
		cancel:     cancel,
	})
	defer func() {
		r.s.inflight.Delete(key)
		cancel(nil)
	}()

	scanID, runErr := r.s.deps.Runner.Run(jobCtx, sched.ID, sched.TargetID, effective)
	completedAt := time.Now().UTC()

	status := model.ScheduleRunSuccess
	if runErr != nil {
		// SCHED1.2c will distinguish blackout-cancel from operator-cancel
		// from real failure. For 1.2b every error path lands as failed.
		status = model.ScheduleRunFailed
		if errors.Is(runErr, context.Canceled) {
			r.s.deps.Log.Info("scan canceled",
				"schedule_id", sched.ID, "target_id", sched.TargetID)
		}
	}

	var scanIDPtr *string
	if scanID != "" {
		scanIDPtr = &scanID
	}
	if err := r.s.deps.SchedStore.RecordRun(ctx, sched.ID, status, scanIDPtr, completedAt); err != nil {
		r.s.deps.Log.Warn("record run", "schedule_id", sched.ID, "err", err)
	}

	next, nerr := r.s.expander.ComputeNextRunAt(sched, tmpl)
	if nerr != nil {
		r.s.deps.Log.Warn("compute next_run_at",
			"schedule_id", sched.ID, "err", nerr)
		return runErr
	}
	if err := r.s.deps.SchedStore.SetNextRunAt(ctx, sched.ID, next); err != nil {
		r.s.deps.Log.Warn("set next_run_at",
			"schedule_id", sched.ID, "err", err)
	}
	return runErr
}
