package intervalsched

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stageFixture copies testdata/legacy_schedule_v1.json to a temp dir and
// returns that dir. Tests mutate the dir freely.
func stageFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "legacy_schedule_v1.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schedule.config.json"), src, 0o600))
	return dir
}

func newBackend(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedTargets(t *testing.T, s *storage.SQLiteStore, values ...string) {
	t.Helper()
	for _, v := range values {
		require.NoError(t, s.CreateTarget(context.Background(), &model.Target{Value: v}))
	}
}

func TestMigrateLegacy_GoldenRoundTrip(t *testing.T) {
	dir := stageFixture(t)
	s := newBackend(t)
	seedTargets(t, s, "a.example", "b.example")

	ctx := context.Background()
	report, err := MigrateLegacyScheduleConfig(ctx, dir, s, silentLogger())
	require.NoError(t, err)
	assert.NotEmpty(t, report.TemplateID)
	assert.Equal(t, 2, report.TargetsMigrated)
	assert.Equal(t, 2, report.SchedulesCreated)
	assert.Empty(t, report.SkippedReason)

	// Sentinel rename happened.
	_, err = os.Stat(filepath.Join(dir, "schedule.config.json"))
	assert.True(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Stat(filepath.Join(dir, "schedule.config.json.migrated"))
	assert.NoError(t, err)

	// Template created with is_system=1.
	tmpl, err := s.Templates().GetByName(ctx, "default")
	require.NoError(t, err)
	assert.True(t, tmpl.IsSystem)
	assert.Equal(t, "FREQ=DAILY", tmpl.RRule)
	require.NotNil(t, tmpl.MaintenanceWindow)
	assert.Equal(t, 2*60*60, tmpl.MaintenanceWindow.DurationSec)

	// Each enabled target has a "default" schedule.
	tgts, err := s.ListTargets(ctx)
	require.NoError(t, err)
	for _, tgt := range tgts {
		got, err := s.Schedules().GetByTargetAndName(ctx, tgt.ID, "default")
		require.NoError(t, err, "target %s missing default schedule", tgt.ID)
		require.NotNil(t, got.TemplateID)
		assert.Equal(t, tmpl.ID, *got.TemplateID)
		assert.True(t, got.Enabled)
	}

	// Defaults table links to the new template.
	defaults, err := s.ScheduleDefaults().Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, defaults.DefaultTemplateID)
	assert.Equal(t, tmpl.ID, *defaults.DefaultTemplateID)
}

func TestMigrateLegacy_Idempotent(t *testing.T) {
	dir := stageFixture(t)
	s := newBackend(t)
	seedTargets(t, s, "a.example")
	ctx := context.Background()

	_, err := MigrateLegacyScheduleConfig(ctx, dir, s, silentLogger())
	require.NoError(t, err)

	report, err := MigrateLegacyScheduleConfig(ctx, dir, s, silentLogger())
	require.NoError(t, err)
	assert.Equal(t, "already_migrated", report.SkippedReason)
	assert.Empty(t, report.TemplateID)
}

func TestMigrateLegacy_NoLegacyFile(t *testing.T) {
	dir := t.TempDir()
	s := newBackend(t)
	ctx := context.Background()

	report, err := MigrateLegacyScheduleConfig(ctx, dir, s, silentLogger())
	require.NoError(t, err)
	assert.Equal(t, "no_legacy_file", report.SkippedReason)

	// DB untouched — no default template.
	_, err = s.Templates().GetByName(ctx, "default")
	assert.True(t, errors.Is(err, storage.ErrNotFound))
}

// faultyBackend wraps a real store but decorates TxStores.Schedules with
// a fault-injecting ScheduleStore that fails the Nth Create call.
type faultyBackend struct {
	inner     *storage.SQLiteStore
	failOnNth int // 1-indexed
}

func (f *faultyBackend) ListTargets(ctx context.Context) ([]model.Target, error) {
	return f.inner.ListTargets(ctx)
}

func (f *faultyBackend) Transact(ctx context.Context, fn func(context.Context, storage.TxStores) error) error {
	return f.inner.Transact(ctx, func(ctx context.Context, stores storage.TxStores) error {
		stores.Schedules = &faultyScheduleStore{inner: stores.Schedules, failOnNth: f.failOnNth}
		return fn(ctx, stores)
	})
}

type faultyScheduleStore struct {
	inner     storage.ScheduleStore
	failOnNth int
	calls     int
}

var errInjected = errors.New("injected failure")

func (f *faultyScheduleStore) Create(ctx context.Context, s *model.Schedule) error {
	f.calls++
	if f.calls == f.failOnNth {
		return errInjected
	}
	return f.inner.Create(ctx, s)
}

// Pass-throughs for the other interface methods.
func (f *faultyScheduleStore) Get(ctx context.Context, id string) (*model.Schedule, error) {
	return f.inner.Get(ctx, id)
}
func (f *faultyScheduleStore) GetByTargetAndName(ctx context.Context, t, n string) (*model.Schedule, error) {
	return f.inner.GetByTargetAndName(ctx, t, n)
}
func (f *faultyScheduleStore) ListByTarget(ctx context.Context, t string) ([]model.Schedule, error) {
	return f.inner.ListByTarget(ctx, t)
}
func (f *faultyScheduleStore) ListAll(ctx context.Context) ([]model.Schedule, error) {
	return f.inner.ListAll(ctx)
}
func (f *faultyScheduleStore) ListDue(ctx context.Context, now time.Time, limit int) ([]model.Schedule, error) {
	return f.inner.ListDue(ctx, now, limit)
}
func (f *faultyScheduleStore) ListByTemplate(ctx context.Context, id string) ([]model.Schedule, error) {
	return f.inner.ListByTemplate(ctx, id)
}
func (f *faultyScheduleStore) Update(ctx context.Context, s *model.Schedule) error {
	return f.inner.Update(ctx, s)
}
func (f *faultyScheduleStore) SetNextRunAt(ctx context.Context, id string, next *time.Time) error {
	return f.inner.SetNextRunAt(ctx, id, next)
}
func (f *faultyScheduleStore) RecordRun(ctx context.Context, id string, status model.ScheduleRunStatus, scanID *string, at time.Time) error {
	return f.inner.RecordRun(ctx, id, status, scanID, at)
}
func (f *faultyScheduleStore) Delete(ctx context.Context, id string) error {
	return f.inner.Delete(ctx, id)
}
func (f *faultyScheduleStore) CountByTarget(ctx context.Context, t string) (int, error) {
	return f.inner.CountByTarget(ctx, t)
}

func TestMigrateLegacy_RollbackOnMidStreamFailure(t *testing.T) {
	dir := stageFixture(t)
	s := newBackend(t)
	seedTargets(t, s, "a.example", "b.example", "c.example")
	ctx := context.Background()

	backend := &faultyBackend{inner: s, failOnNth: 2}
	_, err := MigrateLegacyScheduleConfig(ctx, dir, backend, silentLogger())
	require.Error(t, err)
	assert.True(t, errors.Is(err, errInjected))

	// DB rolled back — no template, no schedules for any target.
	_, err = s.Templates().GetByName(ctx, "default")
	assert.True(t, errors.Is(err, storage.ErrNotFound))
	tgts, err := s.ListTargets(ctx)
	require.NoError(t, err)
	for _, tgt := range tgts {
		count, err := s.Schedules().CountByTarget(ctx, tgt.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "target %s should have zero schedules post-rollback", tgt.ID)
	}

	// File system: source is still there, sentinel is not — rollback is
	// all-or-nothing.
	_, err = os.Stat(filepath.Join(dir, "schedule.config.json"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "schedule.config.json.migrated"))
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestDurationToRRule(t *testing.T) {
	cases := []struct {
		d       time.Duration
		want    string
		wantErr error
	}{
		{15 * time.Minute, "FREQ=MINUTELY;INTERVAL=15", nil},
		{time.Hour, "FREQ=HOURLY", nil},
		{6 * time.Hour, "FREQ=HOURLY;INTERVAL=6", nil},
		{24 * time.Hour, "FREQ=DAILY", nil},
		{7 * 24 * time.Hour, "FREQ=WEEKLY", nil},
		{30 * 24 * time.Hour, "FREQ=MONTHLY", nil},
		{90 * time.Minute, "FREQ=DAILY", nil}, // pathological fallback
		{time.Second, "", ErrInvalidLegacyInterval},
		{0, "", ErrInvalidLegacyInterval},
	}
	for _, c := range cases {
		t.Run(c.d.String(), func(t *testing.T) {
			got, err := durationToRRule(c.d)
			if c.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, c.wantErr))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestMigrateLegacy_NotCalledFromBoot is a grep invariant: the legacy
// migration function must not be invoked anywhere in the daemon boot
// path before PR SCHED1.2. This test pins the contract by asserting
// scheduler.go has no reference to MigrateLegacyScheduleConfig.
func TestMigrateLegacy_NotCalledFromBoot(t *testing.T) {
	b, err := os.ReadFile("scheduler.go")
	require.NoError(t, err)
	assert.NotContains(t, string(b), "MigrateLegacyScheduleConfig")
}
