package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func seedTarget(t *testing.T, s *SQLiteStore, value string) *model.Target {
	t.Helper()
	tgt := &model.Target{Value: value}
	require.NoError(t, s.CreateTarget(context.Background(), tgt))
	return tgt
}

func newSchedule(target *model.Target, name string) *model.Schedule {
	return &model.Schedule{
		TargetID: target.ID,
		Name:     name,
		RRule:    "FREQ=DAILY;BYHOUR=2",
		DTStart:  time.Now().UTC(),
		Timezone: "UTC",
		ToolConfig: model.ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical"]}`),
		},
		Enabled: true,
	}
}

func TestScheduleStore_CRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()

	tgt := seedTarget(t, s, "example.com")
	sched := newSchedule(tgt, "default")

	require.NoError(t, store.Create(ctx, sched))
	assert.NotEmpty(t, sched.ID)

	got, err := store.Get(ctx, sched.ID)
	require.NoError(t, err)
	assert.Equal(t, sched.TargetID, got.TargetID)
	assert.Equal(t, "default", got.Name)
	assert.Equal(t, "FREQ=DAILY;BYHOUR=2", got.RRule)
	assert.True(t, got.Enabled)
	assert.NotNil(t, got.ToolConfig["nuclei"])

	got.RRule = "FREQ=HOURLY"
	got.Overrides = []string{"rrule"}
	require.NoError(t, store.Update(ctx, got))

	reread, err := store.Get(ctx, sched.ID)
	require.NoError(t, err)
	assert.Equal(t, "FREQ=HOURLY", reread.RRule)
	assert.Equal(t, []string{"rrule"}, reread.Overrides)

	byName, err := store.GetByTargetAndName(ctx, tgt.ID, "default")
	require.NoError(t, err)
	assert.Equal(t, sched.ID, byName.ID)

	require.NoError(t, store.Delete(ctx, sched.ID))
	_, err = store.Get(ctx, sched.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestScheduleStore_UniqueTargetName(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	require.NoError(t, store.Create(ctx, newSchedule(tgt, "default")))
	err := store.Create(ctx, newSchedule(tgt, "default"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExists))
}

// seedRawTemplate inserts a minimal scan_templates row via raw SQL so
// schedule tests can exercise template_id joins without depending on
// the TemplateStore (which lands in the next commit).
func seedRawTemplate(t *testing.T, s *SQLiteStore, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO scan_templates (id, name, description, rrule, timezone, tool_config, is_system, created_at, updated_at)
		 VALUES (?, ?, '', 'FREQ=DAILY', 'UTC', '{}', 0, ?, ?)`,
		id, name, now, now)
	require.NoError(t, err)
}

func TestScheduleStore_ListByTargetAndTemplate(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()

	tgt := seedTarget(t, s, "example.com")
	tmplID := "tmpl-01"
	seedRawTemplate(t, s, tmplID, "daily")

	s1 := newSchedule(tgt, "a")
	s1.TemplateID = &tmplID
	require.NoError(t, store.Create(ctx, s1))

	s2 := newSchedule(tgt, "b")
	require.NoError(t, store.Create(ctx, s2))

	byTarget, err := store.ListByTarget(ctx, tgt.ID)
	require.NoError(t, err)
	assert.Len(t, byTarget, 2)

	byTemplate, err := store.ListByTemplate(ctx, tmplID)
	require.NoError(t, err)
	assert.Len(t, byTemplate, 1)
	assert.Equal(t, "a", byTemplate[0].Name)

	count, err := store.CountByTarget(ctx, tgt.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestScheduleStore_ListDue(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	duePast := newSchedule(tgt, "due-past")
	duePast.NextRunAt = &past
	require.NoError(t, store.Create(ctx, duePast))

	notYet := newSchedule(tgt, "not-yet")
	notYet.NextRunAt = &future
	require.NoError(t, store.Create(ctx, notYet))

	disabled := newSchedule(tgt, "disabled")
	disabled.Enabled = false
	disabled.NextRunAt = &past
	require.NoError(t, store.Create(ctx, disabled))

	due, err := store.ListDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "due-past", due[0].Name)
}

func TestScheduleStore_SetNextRunAtAndRecordRun(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")
	sched := newSchedule(tgt, "default")
	require.NoError(t, store.Create(ctx, sched))

	next := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, store.SetNextRunAt(ctx, sched.ID, &next))

	got, err := store.Get(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, got.NextRunAt)
	assert.WithinDuration(t, next, *got.NextRunAt, time.Second)

	// RecordRun sets last_scan_id which has an FK → scans(id). Create a
	// real scan row so the FK resolves.
	scan := &model.Scan{TargetID: tgt.ID, Type: model.ScanTypeFull, Status: model.ScanStatusCompleted}
	require.NoError(t, s.CreateScan(ctx, scan))

	require.NoError(t, store.RecordRun(ctx, sched.ID, model.ScheduleRunSuccess, &scan.ID, time.Now()))
	got, err = store.Get(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastRunStatus)
	assert.Equal(t, model.ScheduleRunSuccess, *got.LastRunStatus)
	assert.Equal(t, scan.ID, *got.LastScanID)
}

func TestScheduleStore_ValidatesOnCreate(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	bad := newSchedule(tgt, "bad")
	bad.RRule = "FREQ=SECONDLY"
	err := store.Create(ctx, bad)
	require.Error(t, err)

	unknownTool := newSchedule(tgt, "amass-schedule")
	unknownTool.ToolConfig = model.ToolConfig{"amass": json.RawMessage(`{}`)}
	err = store.Create(ctx, unknownTool)
	require.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrUnknownTool))
}

func TestScheduleStore_CascadeOnTargetDelete(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")
	require.NoError(t, store.Create(ctx, newSchedule(tgt, "default")))

	require.NoError(t, s.DeleteTarget(ctx, tgt.ID))

	schedules, err := store.ListByTarget(ctx, tgt.ID)
	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestScheduleStore_NullsTemplateOnTemplateDelete(t *testing.T) {
	s := newTestStore(t)
	store := s.Schedules()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	tmplID := "tmpl-01"
	seedRawTemplate(t, s, tmplID, "daily")

	sched := newSchedule(tgt, "default")
	sched.TemplateID = &tmplID
	require.NoError(t, store.Create(ctx, sched))

	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_templates WHERE id = ?`, tmplID)
	require.NoError(t, err)

	got, err := store.Get(ctx, sched.ID)
	require.NoError(t, err)
	assert.Nil(t, got.TemplateID)
}
