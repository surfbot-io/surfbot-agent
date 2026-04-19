package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestScheduleDefaultsStore_SeededOnMigration(t *testing.T) {
	s := newTestStore(t)
	store := s.ScheduleDefaults()
	ctx := context.Background()

	got, err := store.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "FREQ=DAILY;BYHOUR=2", got.DefaultRRule)
	assert.Equal(t, "UTC", got.DefaultTimezone)
	assert.Equal(t, 4, got.MaxConcurrentScans)
	assert.Equal(t, 60, got.JitterSeconds)
	assert.False(t, got.RunOnStart)
}

func TestScheduleDefaultsStore_Update(t *testing.T) {
	s := newTestStore(t)
	store := s.ScheduleDefaults()
	ctx := context.Background()

	tmplID := "tmpl-01"
	seedRawTemplate(t, s, tmplID, "system-default")

	got, err := store.Get(ctx)
	require.NoError(t, err)

	got.DefaultTemplateID = &tmplID
	got.DefaultRRule = "FREQ=HOURLY"
	got.MaxConcurrentScans = 8
	got.RunOnStart = true
	got.JitterSeconds = 30
	got.DefaultToolConfig = model.ToolConfig{
		"nuclei": json.RawMessage(`{"severity":["critical"]}`),
	}
	got.DefaultMaintenanceWindow = &model.MaintenanceWindow{
		RRule: "FREQ=DAILY;BYHOUR=2", DurationSec: 7200, Timezone: "UTC",
	}
	require.NoError(t, store.Update(ctx, got))

	reread, err := store.Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, reread.DefaultTemplateID)
	assert.Equal(t, tmplID, *reread.DefaultTemplateID)
	assert.Equal(t, "FREQ=HOURLY", reread.DefaultRRule)
	assert.Equal(t, 8, reread.MaxConcurrentScans)
	assert.True(t, reread.RunOnStart)
	assert.Equal(t, 30, reread.JitterSeconds)
	assert.Contains(t, reread.DefaultToolConfig, "nuclei")
	require.NotNil(t, reread.DefaultMaintenanceWindow)
	assert.Equal(t, 7200, reread.DefaultMaintenanceWindow.DurationSec)
}

func TestScheduleDefaultsStore_RejectsUnknownTool(t *testing.T) {
	s := newTestStore(t)
	store := s.ScheduleDefaults()
	ctx := context.Background()

	got, err := store.Get(ctx)
	require.NoError(t, err)
	got.DefaultToolConfig = model.ToolConfig{"amass": json.RawMessage(`{}`)}
	err = store.Update(ctx, got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrUnknownTool))
}

func TestScheduleDefaultsStore_SingletonCheck(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedule_defaults (id, default_rrule, default_timezone, default_tool_config,
		                                max_concurrent_scans, run_on_start, jitter_seconds, updated_at)
		 VALUES (2, 'FREQ=DAILY', 'UTC', '{}', 4, 0, 60, '2026-01-01T00:00:00Z')`)
	require.Error(t, err, "CHECK (id = 1) must reject inserts with other ids")
}
