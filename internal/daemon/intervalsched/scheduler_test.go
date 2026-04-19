package intervalsched

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// --- Test fakes -----------------------------------------------------

type tickerScheduleStore struct {
	mu        sync.Mutex
	due       []model.Schedule
	listErr   error
	recorded  []recordedRun
	nextRunAt map[string]*time.Time
}

type recordedRun struct {
	ScheduleID string
	Status     model.ScheduleRunStatus
	ScanID     *string
	At         time.Time
}

func newTickerScheduleStore() *tickerScheduleStore {
	return &tickerScheduleStore{nextRunAt: map[string]*time.Time{}}
}

func (f *tickerScheduleStore) ListDue(_ context.Context, _ time.Time, _ int) ([]model.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]model.Schedule, len(f.due))
	copy(out, f.due)
	// drain so subsequent ticks don't redispatch the same items
	f.due = nil
	return out, nil
}

func (f *tickerScheduleStore) RecordRun(_ context.Context, id string, status model.ScheduleRunStatus, scanID *string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, recordedRun{ScheduleID: id, Status: status, ScanID: scanID, At: at})
	return nil
}

func (f *tickerScheduleStore) SetNextRunAt(_ context.Context, id string, next *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextRunAt[id] = next
	return nil
}

func (f *tickerScheduleStore) snapshotRecorded() []recordedRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRun, len(f.recorded))
	copy(out, f.recorded)
	return out
}

// Unused interface methods (kept simple for the master-ticker tests).
func (f *tickerScheduleStore) Create(context.Context, *model.Schedule) error { return nil }
func (f *tickerScheduleStore) Get(context.Context, string) (*model.Schedule, error) {
	return nil, nil
}
func (f *tickerScheduleStore) GetByTargetAndName(context.Context, string, string) (*model.Schedule, error) {
	return nil, nil
}
func (f *tickerScheduleStore) ListByTarget(context.Context, string) ([]model.Schedule, error) {
	return nil, nil
}
func (f *tickerScheduleStore) ListAll(context.Context) ([]model.Schedule, error) {
	return nil, nil
}
func (f *tickerScheduleStore) ListByTemplate(context.Context, string) ([]model.Schedule, error) {
	return nil, nil
}
func (f *tickerScheduleStore) Update(context.Context, *model.Schedule) error      { return nil }
func (f *tickerScheduleStore) Delete(context.Context, string) error               { return nil }
func (f *tickerScheduleStore) CountByTarget(context.Context, string) (int, error) { return 0, nil }

type tickerTemplateStore struct{}

func (tickerTemplateStore) Create(context.Context, *model.Template) error        { return nil }
func (tickerTemplateStore) Get(context.Context, string) (*model.Template, error) { return nil, nil }
func (tickerTemplateStore) GetByName(context.Context, string) (*model.Template, error) {
	return nil, nil
}
func (tickerTemplateStore) List(context.Context) ([]model.Template, error) { return nil, nil }
func (tickerTemplateStore) Update(context.Context, *model.Template) error  { return nil }
func (tickerTemplateStore) Delete(context.Context, string) error           { return nil }

type tickerBlackoutStore struct {
	windows []model.BlackoutWindow
}

func (f *tickerBlackoutStore) Create(context.Context, *model.BlackoutWindow) error { return nil }
func (f *tickerBlackoutStore) Get(_ context.Context, id string) (*model.BlackoutWindow, error) {
	for i := range f.windows {
		if f.windows[i].ID == id {
			w := f.windows[i]
			return &w, nil
		}
	}
	return nil, nil
}
func (f *tickerBlackoutStore) List(context.Context) ([]model.BlackoutWindow, error) {
	out := make([]model.BlackoutWindow, len(f.windows))
	copy(out, f.windows)
	return out, nil
}
func (f *tickerBlackoutStore) ListByScope(_ context.Context, scope model.BlackoutScope) ([]model.BlackoutWindow, error) {
	out := []model.BlackoutWindow{}
	for _, w := range f.windows {
		if w.Scope == scope {
			out = append(out, w)
		}
	}
	return out, nil
}
func (f *tickerBlackoutStore) ListByTarget(_ context.Context, targetID string) ([]model.BlackoutWindow, error) {
	out := []model.BlackoutWindow{}
	for _, w := range f.windows {
		if w.TargetID != nil && *w.TargetID == targetID {
			out = append(out, w)
		}
	}
	return out, nil
}
func (f *tickerBlackoutStore) ListActive(context.Context, string) ([]model.BlackoutWindow, error) {
	return nil, nil
}
func (f *tickerBlackoutStore) Update(context.Context, *model.BlackoutWindow) error { return nil }
func (f *tickerBlackoutStore) Delete(context.Context, string) error                { return nil }

type fakeDefaultsStore struct {
	defaults model.ScheduleDefaults
}

func (f *fakeDefaultsStore) Get(context.Context) (*model.ScheduleDefaults, error) {
	d := f.defaults
	return &d, nil
}
func (f *fakeDefaultsStore) Update(context.Context, *model.ScheduleDefaults) error { return nil }

type fakeAdHocStore struct{}

func (fakeAdHocStore) Create(context.Context, *model.AdHocScanRun) error        { return nil }
func (fakeAdHocStore) Get(context.Context, string) (*model.AdHocScanRun, error) { return nil, nil }
func (fakeAdHocStore) ListByTarget(context.Context, string, int) ([]model.AdHocScanRun, error) {
	return nil, nil
}
func (fakeAdHocStore) ListByStatus(context.Context, model.AdHocRunStatus) ([]model.AdHocScanRun, error) {
	return nil, nil
}
func (fakeAdHocStore) UpdateStatus(context.Context, string, model.AdHocRunStatus, time.Time) error {
	return nil
}
func (fakeAdHocStore) AttachScan(context.Context, string, string) error { return nil }
func (fakeAdHocStore) Delete(context.Context, string) error             { return nil }

type fakeRunner struct {
	mu       sync.Mutex
	calls    []runnerCall
	delay    time.Duration
	scanID   string
	runErr   error
	blockCh  chan struct{}
	observed bool
}

type runnerCall struct {
	ScheduleID string
	TargetID   string
}

func (r *fakeRunner) Run(ctx context.Context, scheduleID, targetID string, _ model.EffectiveConfig) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, runnerCall{ScheduleID: scheduleID, TargetID: targetID})
	delay := r.delay
	block := r.blockCh
	scanID := r.scanID
	err := r.runErr
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			r.mu.Lock()
			r.observed = true
			r.mu.Unlock()
			return "", ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if scanID == "" {
		scanID = "s_default"
	}
	return scanID, err
}

func (r *fakeRunner) snapshot() []runnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]runnerCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func tickerSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newSchedulerForTest(t *testing.T, deps Dependencies) *Scheduler {
	t.Helper()
	if deps.Log == nil {
		deps.Log = tickerSilentLogger()
	}
	if deps.TickInterval == 0 {
		deps.TickInterval = 10 * time.Millisecond
	}
	s, err := New(deps)
	require.NoError(t, err)
	return s
}

func newDefaults() model.ScheduleDefaults {
	return model.ScheduleDefaults{
		DefaultRRule:       "FREQ=DAILY",
		DefaultTimezone:    "UTC",
		MaxConcurrentScans: 4,
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never met")
}

// --- Tests ----------------------------------------------------------

func TestScheduler_TickDispatchesDue(t *testing.T) {
	store := newTickerScheduleStore()
	store.due = []model.Schedule{
		{ID: "s1", TargetID: "t1", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
		{ID: "s2", TargetID: "t2", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
		{ID: "s3", TargetID: "t3", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
	}
	runner := &fakeRunner{}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: &tickerBlackoutStore{},
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx))

	waitFor(t, func() bool { return len(runner.snapshot()) >= 3 })

	cancel()
	require.NoError(t, s.Stop(context.Background()))

	calls := runner.snapshot()
	require.Len(t, calls, 3)
	seen := map[string]bool{}
	for _, c := range calls {
		seen[c.ScheduleID] = true
	}
	assert.True(t, seen["s1"])
	assert.True(t, seen["s2"])
	assert.True(t, seen["s3"])
}

func TestScheduler_BlackoutSkipRecorded(t *testing.T) {
	store := newTickerScheduleStore()
	store.due = []model.Schedule{
		{ID: "s1", TargetID: "t1", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
	}
	now := time.Now().UTC()
	bo := &tickerBlackoutStore{windows: []model.BlackoutWindow{{
		ID:          "b1",
		Scope:       model.BlackoutScopeGlobal,
		Name:        "always",
		RRule:       "FREQ=MINUTELY",
		DurationSec: 3600,
		Timezone:    "UTC",
		Enabled:     true,
		CreatedAt:   now.Add(-time.Hour),
	}}}
	runner := &fakeRunner{}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: bo,
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx))

	waitFor(t, func() bool { return len(store.snapshotRecorded()) >= 1 })
	cancel()
	require.NoError(t, s.Stop(context.Background()))

	recorded := store.snapshotRecorded()
	require.GreaterOrEqual(t, len(recorded), 1)
	assert.Equal(t, model.ScheduleRunSkippedBlackout, recorded[0].Status)
	assert.Empty(t, runner.snapshot(), "runner must not be invoked during blackout")
}

func TestScheduler_SameTargetSerialized(t *testing.T) {
	store := newTickerScheduleStore()
	store.due = []model.Schedule{
		{ID: "s1", TargetID: "shared", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
		{ID: "s2", TargetID: "shared", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
	}
	runner := &fakeRunner{blockCh: make(chan struct{})}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: &tickerBlackoutStore{},
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx))

	// Wait until at least one job is in flight (lock held).
	waitFor(t, func() bool { return len(runner.snapshot()) >= 1 })
	// Give the loop a chance to attempt the second one (it should fail to
	// acquire the lock).
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, len(runner.snapshot()), "second schedule for same target must be skipped while first is in flight")

	close(runner.blockCh)
	cancel()
	require.NoError(t, s.Stop(context.Background()))
}

func TestScheduler_StopDrains(t *testing.T) {
	store := newTickerScheduleStore()
	store.due = []model.Schedule{
		{ID: "s1", TargetID: "t1", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
	}
	runner := &fakeRunner{delay: 200 * time.Millisecond}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: &tickerBlackoutStore{},
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, s.Start(ctx))
	waitFor(t, func() bool { return len(runner.snapshot()) >= 1 })

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	require.NoError(t, s.Stop(stopCtx))
}

func TestScheduler_ContextCancelsTick(t *testing.T) {
	store := newTickerScheduleStore()
	runner := &fakeRunner{}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: &tickerBlackoutStore{},
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, s.Start(ctx))
	cancel()
	require.NoError(t, s.Stop(context.Background()))
}

func TestScheduler_RunnerErrorRecordedAsFailed(t *testing.T) {
	store := newTickerScheduleStore()
	store.due = []model.Schedule{
		{ID: "s1", TargetID: "t1", RRule: "FREQ=DAILY", Timezone: "UTC", Enabled: true},
	}
	runner := &fakeRunner{runErr: errors.New("boom")}
	s := newSchedulerForTest(t, Dependencies{
		SchedStore:    store,
		TmplStore:     tickerTemplateStore{},
		BlackoutStore: &tickerBlackoutStore{},
		DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
		AdHocStore:    fakeAdHocStore{},
		Runner:        runner,
		JitterSeed:    1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx))

	waitFor(t, func() bool { return len(store.snapshotRecorded()) >= 1 })
	cancel()
	require.NoError(t, s.Stop(context.Background()))

	rec := store.snapshotRecorded()
	require.Len(t, rec, 1)
	assert.Equal(t, model.ScheduleRunFailed, rec[0].Status)
}

func TestNew_RejectsMissingDeps(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Dependencies)
	}{
		{"no SchedStore", func(d *Dependencies) { d.SchedStore = nil }},
		{"no TmplStore", func(d *Dependencies) { d.TmplStore = nil }},
		{"no BlackoutStore", func(d *Dependencies) { d.BlackoutStore = nil }},
		{"no DefaultsStore", func(d *Dependencies) { d.DefaultsStore = nil }},
		{"no Runner", func(d *Dependencies) { d.Runner = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := Dependencies{
				SchedStore:    newTickerScheduleStore(),
				TmplStore:     tickerTemplateStore{},
				BlackoutStore: &tickerBlackoutStore{},
				DefaultsStore: &fakeDefaultsStore{defaults: newDefaults()},
				AdHocStore:    fakeAdHocStore{},
				Runner:        &fakeRunner{},
				Log:           tickerSilentLogger(),
			}
			c.mut(&deps)
			_, err := New(deps)
			require.Error(t, err)
		})
	}
}
