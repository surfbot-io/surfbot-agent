package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// ScheduleDefaultsStore persists the singleton schedule_defaults row that
// supplies inherited values for schedules without explicit overrides.
// The row id is always 1 (enforced by CHECK constraint in the schema).
type ScheduleDefaultsStore interface {
	Get(ctx context.Context) (*model.ScheduleDefaults, error)
	Update(ctx context.Context, d *model.ScheduleDefaults) error
}

// ScheduleDefaults returns a ScheduleDefaultsStore backed by this SQLiteStore.
func (s *SQLiteStore) ScheduleDefaults() ScheduleDefaultsStore {
	return &sqliteScheduleDefaultsStore{db: s.db}
}

type sqliteScheduleDefaultsStore struct {
	db *sql.DB
}

func (st *sqliteScheduleDefaultsStore) Get(ctx context.Context) (*model.ScheduleDefaults, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT default_template_id, default_rrule, default_timezone, default_tool_config,
		        default_maintenance_window, max_concurrent_scans, run_on_start,
		        jitter_seconds, updated_at
		 FROM schedule_defaults WHERE id = 1`)
	var (
		d         model.ScheduleDefaults
		tmplID    sql.NullString
		toolJSON  string
		mwJSON    sql.NullString
		runOnStart int
		updatedAt sql.NullString
	)
	if err := row.Scan(
		&tmplID, &d.DefaultRRule, &d.DefaultTimezone, &toolJSON, &mwJSON,
		&d.MaxConcurrentScans, &runOnStart, &d.JitterSeconds, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store.schedule_defaults.Get: %w", err)
	}
	d.RunOnStart = runOnStart != 0
	d.UpdatedAt = parseTime(updatedAt)
	if tmplID.Valid && tmplID.String != "" {
		v := tmplID.String
		d.DefaultTemplateID = &v
	}
	if toolJSON == "" {
		d.DefaultToolConfig = model.ToolConfig{}
	} else {
		if err := json.Unmarshal([]byte(toolJSON), &d.DefaultToolConfig); err != nil {
			return nil, fmt.Errorf("unmarshaling default_tool_config: %w", err)
		}
	}
	if mwJSON.Valid && mwJSON.String != "" {
		var mw model.MaintenanceWindow
		if err := json.Unmarshal([]byte(mwJSON.String), &mw); err != nil {
			return nil, fmt.Errorf("unmarshaling default_maintenance_window: %w", err)
		}
		d.DefaultMaintenanceWindow = &mw
	}
	return &d, nil
}

func (st *sqliteScheduleDefaultsStore) Update(ctx context.Context, d *model.ScheduleDefaults) error {
	if d == nil {
		return fmt.Errorf("defaults is nil")
	}
	if err := model.ValidateToolConfig(d.DefaultToolConfig); err != nil {
		return err
	}
	d.UpdatedAt = time.Now().UTC()

	toolJSON, err := json.Marshal(d.DefaultToolConfig)
	if err != nil {
		return fmt.Errorf("marshaling default_tool_config: %w", err)
	}
	var mwJSON any
	if d.DefaultMaintenanceWindow != nil {
		b, err := json.Marshal(d.DefaultMaintenanceWindow)
		if err != nil {
			return fmt.Errorf("marshaling default_maintenance_window: %w", err)
		}
		mwJSON = string(b)
	}

	res, err := st.db.ExecContext(ctx,
		`UPDATE schedule_defaults SET
		   default_template_id = ?, default_rrule = ?, default_timezone = ?,
		   default_tool_config = ?, default_maintenance_window = ?,
		   max_concurrent_scans = ?, run_on_start = ?, jitter_seconds = ?,
		   updated_at = ?
		 WHERE id = 1`,
		nullableString(d.DefaultTemplateID), d.DefaultRRule, d.DefaultTimezone,
		string(toolJSON), mwJSON, d.MaxConcurrentScans,
		boolToInt(d.RunOnStart), d.JitterSeconds,
		d.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("store.schedule_defaults.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Should never happen — the migration seeds the row.
		return ErrNotFound
	}
	return nil
}
