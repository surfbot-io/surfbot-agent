//go:build integration

package intervalsched

// SCHED1.2c integration test (R14): exercises the master ticker against
// a real :memory: SQLite and the production stores. Three scenarios in
// one file, total wall-clock budget ≤ 10 s. Detection tools are NOT
// invoked — the ScanRunner is a fake that records calls and respects
// ctx, so this is end-to-end through the scheduler / stores layer
// without external network or binary dependencies.
//
// Run with: go test -tags=integration ./internal/daemon/intervalsched/... -race -count=1

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// integScanRunner satisfies ScanRunner. Optionally creates a real scan
// row in the store so RecordRun's FK on scan_schedules.last_scan_id is
// satisfied without invoking the real pipeline.
type integScanRunner struct {
	store    *storage.SQLiteStore
	mu       sync.Mutex
	calls    []string
	scanIDs  []string
	delay    time.Duration
	blockCh  chan struct{}
	observed atomic.Bool
}

func (r *integScanRunner) Run(ctx context.Context, scheduleID, targetID string, _ model.EffectiveConfig) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, scheduleID+":"+targetID)
	delay := r.delay
	block := r.blockCh
	r.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			r.observed.Store(true)
			return "", ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			r.observed.Store(true)
			return "", ctx.Err()
		}
	}

	// Create a real scans row so RecordRun's FK constraint is satisfied.
	now := time.Now().UTC()
	scan := &model.Scan{
		TargetID:   targetID,
		Type:       model.ScanTypeFull,
		Status:     model.ScanStatusCompleted,
		StartedAt:  &now,
		FinishedAt: &now,
	}
	if err := r.store.CreateScan(ctx, scan); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.scanIDs = append(r.scanIDs, scan.ID)
	r.mu.Unlock()
	return scan.ID, nil
}

func (r *integScanRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newIntegStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	// :memory: SQLite is per-connection; the production *sql.DB pool
	// defeats schema sharing. Use a file in the test's temp dir so all
	// pool connections see the same DB.
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "integ.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newIntegScheduler(t *testing.T, store *storage.SQLiteStore, runner ScanRunner) *Scheduler {
	t.Helper()
	s, err := New(Dependencies{
		SchedStore:    store.Schedules(),
		TmplStore:     store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
		Runner:        runner,
		TickInterval:  100 * time.Millisecond,
		JitterSeed:    1,
	})
	require.NoError(t, err)
	return s
}

func TestIntegration_EndToEnd(t *testing.T) {
	t.Parallel()
	store := newIntegStore(t)
	ctx := context.Background()

	// Two targets, three schedules. All due immediately so the first tick
	// dispatches every one of them. ScanRunner returns quickly.
	now := time.Now().UTC()
	due := now
	for _, tgt := range []string{"alpha.example", "beta.example"} {
		require.NoError(t, store.CreateTarget(ctx, &model.Target{Value: tgt, Enabled: true}))
	}
	targets, err := store.ListTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 2)

	for i, tgt := range targets {
		// Two schedules on alpha, one on beta — exercises per-target
		// serialization (lock).
		count := 1
		if i == 0 {
			count = 2
		}
		for j := 0; j < count; j++ {
			name := "sched"
			if j == 1 {
				name = "sched2"
			}
			require.NoError(t, store.Schedules().Create(ctx, &model.Schedule{
				TargetID:  tgt.ID,
				Name:      name,
				RRule:     "FREQ=DAILY",
				DTStart:   now,
				Timezone:  "UTC",
				Enabled:   true,
				NextRunAt: &due,
			}))
		}
	}

	runner := &integScanRunner{store: store}
	s := newIntegScheduler(t, store, runner)
	require.NoError(t, s.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(stopCtx)
	}()

	// 3 schedules, each fires once. Allow several ticks (per-target lock
	// serializes the two schedules on alpha so they fire across at least
	// two ticks).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && runner.callCount() < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, runner.callCount(), 3, "all 3 schedules must fire")

	// Each schedule's last_run_status must be success.
	all, err := store.Schedules().ListAll(ctx)
	require.NoError(t, err)
	for _, sched := range all {
		assert.NotNil(t, sched.LastRunStatus, "schedule %s missing LastRunStatus", sched.Name)
		if sched.LastRunStatus != nil {
			assert.Equal(t, model.ScheduleRunSuccess, *sched.LastRunStatus,
				"schedule %s status: %v", sched.Name, *sched.LastRunStatus)
		}
	}
}

func TestIntegration_PauseInFlight(t *testing.T) {
	t.Parallel()
	store := newIntegStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateTarget(ctx, &model.Target{Value: "slow.example", Enabled: true}))
	targets, err := store.ListTargets(ctx)
	require.NoError(t, err)
	tgt := targets[0]

	due := time.Now().UTC()
	require.NoError(t, store.Schedules().Create(ctx, &model.Schedule{
		TargetID:  tgt.ID,
		Name:      "slow",
		RRule:     "FREQ=DAILY",
		DTStart:   due,
		Timezone:  "UTC",
		Enabled:   true,
		NextRunAt: &due,
	}))

	// blockCh keeps the scan running until either ctx fires or the test
	// closes the channel. The blackout-driven cancel is what we want here.
	runner := &integScanRunner{store: store, blockCh: make(chan struct{})}
	s := newIntegScheduler(t, store, runner)
	require.NoError(t, s.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(stopCtx)
	}()

	// Wait for the scan to be in flight.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runner.callCount() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, 1, runner.callCount(), "scan must be in flight")

	// Insert a blackout that activates ~1s in the future. The storage
	// layer overwrites CreatedAt at Create(), so we encode the start
	// time as an explicit DTSTART in the RRULE — the BlackoutEvaluator
	// honors RRULE-supplied DTSTART over CreatedAt fallback.
	startAt := time.Now().UTC().Add(1 * time.Second).Truncate(time.Second)
	rrule := "DTSTART:" + startAt.Format("20060102T150405Z") + "\nRRULE:FREQ=DAILY;COUNT=1"
	require.NoError(t, store.Blackouts().Create(ctx, &model.BlackoutWindow{
		Scope:       model.BlackoutScopeGlobal,
		Name:        "pause-now",
		RRule:       rrule,
		DurationSec: 600,
		Timezone:    "UTC",
		Enabled:     true,
	}))

	// Wait for the schedule to be marked paused_blackout.
	deadline = time.Now().Add(5 * time.Second)
	var sawPaused bool
	for time.Now().Before(deadline) {
		all, err := store.Schedules().ListAll(ctx)
		require.NoError(t, err)
		for _, sched := range all {
			if sched.LastRunStatus != nil && *sched.LastRunStatus == model.ScheduleRunPausedBlackout {
				sawPaused = true
				break
			}
		}
		if sawPaused {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.True(t, sawPaused, "scan must be marked paused_blackout once blackout activates mid-flight")
	assert.True(t, runner.observed.Load(), "scan runner must observe ctx cancel")
}

func TestIntegration_AdHoc(t *testing.T) {
	t.Parallel()
	store := newIntegStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateTarget(ctx, &model.Target{Value: "adhoc.example", Enabled: true}))
	targets, err := store.ListTargets(ctx)
	require.NoError(t, err)
	tgt := targets[0]

	runner := &integScanRunner{store: store, delay: 50 * time.Millisecond}
	s := newIntegScheduler(t, store, runner)
	require.NoError(t, s.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(stopCtx)
	}()

	run := model.AdHocScanRun{
		TargetID:    tgt.ID,
		InitiatedBy: "test:integration",
		Status:      model.AdHocPending,
		RequestedAt: time.Now().UTC(),
	}
	require.NoError(t, store.AdHocScanRuns().Create(ctx, &run))

	scanID, err := s.DispatchAdHoc(ctx, run)
	require.NoError(t, err)
	assert.NotEmpty(t, scanID)

	// ad_hoc_scan_runs row must reflect completion + scan_id attached.
	persisted, err := store.AdHocScanRuns().Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AdHocCompleted, persisted.Status)
	require.NotNil(t, persisted.ScanID)
	assert.Equal(t, scanID, *persisted.ScanID)
}
