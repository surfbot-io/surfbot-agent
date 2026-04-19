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

	"github.com/google/uuid"

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
// ad-hoc jobs use the "adhoc:" prefix so both kinds coexist in the
// in-flight table without collision.
func jobKey(scheduleID string) string { return scheduleID }

func adHocJobKey(adHocID string) string { return "adhoc:" + adHocID }

// ErrTargetBusy is returned by DispatchAdHoc when the target's
// per-target lock is already held by another in-flight scan.
var ErrTargetBusy = errors.New("target busy")

// ErrInBlackout is returned by DispatchAdHoc when the target is inside
// an active blackout window at dispatch time. Ad-hoc dispatches refuse
// to run during a blackout — operators who insist must wait for the
// window to close.
var ErrInBlackout = errors.New("target in blackout")

// adHocPayload wraps an AdHocScanRun with a synchronous result channel
// so DispatchAdHoc can block until the worker pool finishes the scan.
// The pool dispatches Jobs with this in Payload; scanJobRunner
// type-switches on the payload to drive the ad-hoc bookkeeping path
// (ad_hoc_scan_runs row updates) instead of the schedule path.
type adHocPayload struct {
	run    model.AdHocScanRun
	result chan adHocResult
}

type adHocResult struct {
	scanID string
	err    error
}

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

// DispatchAdHoc dispatches an ad-hoc scan for a target. It honors
// per-target serialization (waits for the target lock — non-blocking,
// returns ErrTargetBusy on contention) and refuses to dispatch while a
// blackout covers the target (returns ErrInBlackout). Ad-hoc runs
// participate in pause-in-flight: an active mid-scan blackout cancels
// their ctx the same way scheduled scans are canceled.
//
// The method blocks until the underlying scan completes and returns
// the new scan's ID. Callers that want async semantics (e.g. the HTTP
// trigger handler) wrap the call in a goroutine.
//
// On dispatch the ad_hoc_scan_runs row transitions Pending → Running;
// on completion it transitions to Completed (with scan_id attached) or
// Failed.
func (s *Scheduler) DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (string, error) {
	if run.TargetID == "" {
		return "", fmt.Errorf("DispatchAdHoc: target_id is required")
	}
	if !s.locks.TryAcquire(run.TargetID) {
		return "", ErrTargetBusy
	}
	now := s.deps.Clock.Now()
	active, _ := s.blackouts.IsActive(run.TargetID, now)
	if active {
		s.locks.Release(run.TargetID)
		return "", ErrInBlackout
	}
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	// Mark the row as running. The lock is held; the worker pool runs the
	// scan on a goroutine and signals completion via adHocPayload.result.
	if s.deps.AdHocStore != nil {
		_ = s.deps.AdHocStore.UpdateStatus(ctx, run.ID, model.AdHocRunning, now.UTC())
	}

	resultCh := make(chan adHocResult, 1)
	job := Job{
		ScheduleID: adHocJobKey(run.ID),
		TargetID:   run.TargetID,
		Payload:    &adHocPayload{run: run, result: resultCh},
	}
	if !s.pool.Dispatch(job) {
		s.locks.Release(run.TargetID)
		if s.deps.AdHocStore != nil {
			_ = s.deps.AdHocStore.UpdateStatus(ctx, run.ID, model.AdHocFailed, time.Now().UTC())
		}
		return "", fmt.Errorf("worker pool full; ad-hoc dispatch rejected")
	}

	select {
	case res := <-resultCh:
		return res.scanID, res.err
	case <-ctx.Done():
		// Caller canceled; the worker keeps running until the scan
		// completes on its own. We surface the ctx error to the caller
		// but the ad_hoc_scan_runs row will still get updated by the
		// worker.
		return "", ctx.Err()
	}
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

// ErrBlackoutPause is attached to in-flight job ctx via
// context.WithCancelCause when a blackout activates mid-scan.
// scanJobRunner inspects context.Cause(ctx) to distinguish this cause
// from a normal shutdown / operator cancel and records the schedule run
// as ScheduleRunPausedBlackout on completion.
var ErrBlackoutPause = errors.New("blackout activated")

// evaluateInFlightBlackouts walks the in-flight job table and cancels
// the ctx of any job whose target has entered a blackout since dispatch.
// Jobs that were already in a blackout at dispatch time (defensive —
// the ticker shouldn't have dispatched them) are left alone so the same
// blackout doesn't double-cancel them.
func (s *Scheduler) evaluateInFlightBlackouts(now time.Time) {
	s.inflight.Range(func(_, value any) bool {
		job := value.(*inflightJob)
		activeNow, _ := s.blackouts.IsActive(job.targetID, now)
		if !activeNow {
			return true
		}
		activeThen, _ := s.blackouts.IsActive(job.targetID, job.acquiredAt)
		if activeThen {
			// Pre-existing blackout — the ticker shouldn't have
			// dispatched this. Don't double-cancel; just log so the
			// regression is observable.
			s.deps.Log.Warn("blackout active at dispatch time; in-flight check skipped",
				"schedule_id", job.scheduleID, "target_id", job.targetID)
			return true
		}
		s.deps.Log.Info("scan paused by blackout",
			"schedule_id", job.scheduleID, "target_id", job.targetID)
		job.cancel(ErrBlackoutPause)
		return true
	})
}

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

// runAdHoc handles a worker job whose payload is an *adHocPayload.
// Resolves EffectiveConfig from the AdHocScanRun (template + defaults
// + inline overrides), runs the scan with pause-in-flight ctx tracking,
// updates ad_hoc_scan_runs on completion, and signals the originating
// DispatchAdHoc caller via payload.result.
func (r *scanJobRunner) runAdHoc(ctx context.Context, job Job, ah *adHocPayload) error {
	run := ah.run
	tmpl := r.s.loadTemplate(ctx, run.TemplateID)
	// Synthesize a Schedule shell so ResolveEffectiveConfig works against
	// the same cascade rules as scheduled scans.
	pseudo := model.Schedule{
		TargetID:   run.TargetID,
		ToolConfig: run.ToolConfig,
		TemplateID: run.TemplateID,
		Timezone:   r.s.defaults.DefaultTimezone,
		RRule:      r.s.defaults.DefaultRRule,
	}
	effective, err := model.ResolveEffectiveConfig(pseudo, tmpl, r.s.defaults)
	if err != nil {
		ah.result <- adHocResult{err: fmt.Errorf("resolve effective config: %w", err)}
		if r.s.deps.AdHocStore != nil {
			_ = r.s.deps.AdHocStore.UpdateStatus(context.Background(), run.ID, model.AdHocFailed, time.Now().UTC())
		}
		return err
	}

	jobCtx, cancel := context.WithCancelCause(ctx)
	key := adHocJobKey(run.ID)
	r.s.inflight.Store(key, &inflightJob{
		scheduleID: run.ID,
		targetID:   run.TargetID,
		acquiredAt: r.s.deps.Clock.Now(),
		cancel:     cancel,
	})
	defer func() {
		r.s.inflight.Delete(key)
		cancel(nil)
	}()

	scanID, runErr := r.s.deps.Runner.Run(jobCtx, job.ScheduleID, run.TargetID, effective)
	completedAt := time.Now().UTC()

	finalStatus := model.AdHocCompleted
	if runErr != nil {
		finalStatus = model.AdHocFailed
		if errors.Is(runErr, context.Canceled) && errors.Is(context.Cause(jobCtx), ErrBlackoutPause) {
			r.s.deps.Log.Info("ad-hoc scan paused by blackout",
				"adhoc_id", run.ID, "target_id", run.TargetID)
		}
	}

	if r.s.deps.AdHocStore != nil {
		if scanID != "" {
			_ = r.s.deps.AdHocStore.AttachScan(context.Background(), run.ID, scanID)
		}
		_ = r.s.deps.AdHocStore.UpdateStatus(context.Background(), run.ID, finalStatus, completedAt)
	}

	ah.result <- adHocResult{scanID: scanID, err: runErr}
	return runErr
}

func (r *scanJobRunner) Run(ctx context.Context, job Job) error {
	defer r.s.locks.Release(job.TargetID)

	if adhoc, ok := job.Payload.(*adHocPayload); ok {
		return r.runAdHoc(ctx, job, adhoc)
	}

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
		status = model.ScheduleRunFailed
		if errors.Is(runErr, context.Canceled) {
			if errors.Is(context.Cause(jobCtx), ErrBlackoutPause) {
				status = model.ScheduleRunPausedBlackout
				r.s.deps.Log.Info("scan paused by blackout (final)",
					"schedule_id", sched.ID, "target_id", sched.TargetID)
			} else {
				r.s.deps.Log.Info("scan canceled",
					"schedule_id", sched.ID, "target_id", sched.TargetID)
			}
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
