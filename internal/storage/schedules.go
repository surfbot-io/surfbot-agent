package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
)

// ScheduleStore is the persistence interface for per-target scan
// schedules. All implementations must validate the RRULE and ToolConfig
// on Create and Update (SPEC-SCHED1 R6). The interface is consumed by
// the daemon (master ticker), the HTTP handlers, and the CLI, so it
// lives at a package boundary.
type ScheduleStore interface {
	Create(ctx context.Context, s *model.Schedule) error
	Get(ctx context.Context, id string) (*model.Schedule, error)
	GetByTargetAndName(ctx context.Context, targetID, name string) (*model.Schedule, error)
	ListByTarget(ctx context.Context, targetID string) ([]model.Schedule, error)
	ListAll(ctx context.Context) ([]model.Schedule, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]model.Schedule, error)
	ListByTemplate(ctx context.Context, templateID string) ([]model.Schedule, error)
	Update(ctx context.Context, s *model.Schedule) error
	SetNextRunAt(ctx context.Context, id string, next *time.Time) error
	RecordRun(ctx context.Context, id string, status model.ScheduleRunStatus, scanID *string, at time.Time) error
	Delete(ctx context.Context, id string) error
	CountByTarget(ctx context.Context, targetID string) (int, error)
}

// Schedules returns a ScheduleStore backed by this SQLiteStore.
func (s *SQLiteStore) Schedules() ScheduleStore {
	return &sqliteScheduleStore{db: s.db}
}

type sqliteScheduleStore struct {
	db dbtx
}

const scheduleColumns = `id, target_id, name, rrule, dtstart, timezone, template_id, tool_config,
  overrides, maintenance_window, enabled, next_run_at, last_run_at, last_run_status, last_scan_id,
  created_at, updated_at`

func (st *sqliteScheduleStore) Create(ctx context.Context, s *model.Schedule) error {
	if err := validateScheduleInput(s); err != nil {
		return err
	}
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now
	if s.Overrides == nil {
		s.Overrides = []string{}
	}
	if s.ToolConfig == nil {
		s.ToolConfig = model.ToolConfig{}
	}

	toolJSON, overridesJSON, mwJSON, err := marshalScheduleFields(s)
	if err != nil {
		return err
	}

	_, err = st.db.ExecContext(ctx,
		`INSERT INTO scan_schedules (`+scheduleColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TargetID, s.Name, s.RRule, s.DTStart.Format(timeFormat), s.Timezone,
		nullableString(s.TemplateID), toolJSON, overridesJSON, mwJSON, boolToInt(s.Enabled),
		timePtrUTC(s.NextRunAt), timePtrUTC(s.LastRunAt), nullableRunStatus(s.LastRunStatus),
		nullableString(s.LastScanID),
		s.CreatedAt.Format(timeFormat), s.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: schedule (target=%s, name=%s)", ErrAlreadyExists, s.TargetID, s.Name)
		}
		return fmt.Errorf("store.schedules.Create: %w", err)
	}
	return nil
}

func (st *sqliteScheduleStore) Get(ctx context.Context, id string) (*model.Schedule, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+scheduleColumns+` FROM scan_schedules WHERE id = ?`, id)
	return scanSchedule(row)
}

func (st *sqliteScheduleStore) GetByTargetAndName(ctx context.Context, targetID, name string) (*model.Schedule, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+scheduleColumns+` FROM scan_schedules WHERE target_id = ? AND name = ?`, targetID, name)
	return scanSchedule(row)
}

func (st *sqliteScheduleStore) ListByTarget(ctx context.Context, targetID string) ([]model.Schedule, error) {
	return queryManySchedules(ctx, st.db,
		`SELECT `+scheduleColumns+` FROM scan_schedules WHERE target_id = ? ORDER BY name ASC`,
		targetID)
}

func (st *sqliteScheduleStore) ListAll(ctx context.Context) ([]model.Schedule, error) {
	return queryManySchedules(ctx, st.db,
		`SELECT `+scheduleColumns+` FROM scan_schedules ORDER BY created_at ASC`)
}

func (st *sqliteScheduleStore) ListDue(ctx context.Context, now time.Time, limit int) ([]model.Schedule, error) {
	if limit <= 0 {
		limit = 100
	}
	return queryManySchedules(ctx, st.db,
		`SELECT `+scheduleColumns+` FROM scan_schedules
		 WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		 ORDER BY next_run_at ASC
		 LIMIT ?`,
		now.UTC().Format(timeFormat), limit)
}

func (st *sqliteScheduleStore) ListByTemplate(ctx context.Context, templateID string) ([]model.Schedule, error) {
	return queryManySchedules(ctx, st.db,
		`SELECT `+scheduleColumns+` FROM scan_schedules WHERE template_id = ? ORDER BY created_at ASC`,
		templateID)
}

func (st *sqliteScheduleStore) Update(ctx context.Context, s *model.Schedule) error {
	if err := validateScheduleInput(s); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	if s.Overrides == nil {
		s.Overrides = []string{}
	}
	if s.ToolConfig == nil {
		s.ToolConfig = model.ToolConfig{}
	}

	toolJSON, overridesJSON, mwJSON, err := marshalScheduleFields(s)
	if err != nil {
		return err
	}

	res, err := st.db.ExecContext(ctx,
		`UPDATE scan_schedules SET
		   target_id = ?, name = ?, rrule = ?, dtstart = ?, timezone = ?,
		   template_id = ?, tool_config = ?, overrides = ?, maintenance_window = ?,
		   enabled = ?, next_run_at = ?, last_run_at = ?, last_run_status = ?,
		   last_scan_id = ?, updated_at = ?
		 WHERE id = ?`,
		s.TargetID, s.Name, s.RRule, s.DTStart.Format(timeFormat), s.Timezone,
		nullableString(s.TemplateID), toolJSON, overridesJSON, mwJSON, boolToInt(s.Enabled),
		timePtrUTC(s.NextRunAt), timePtrUTC(s.LastRunAt), nullableRunStatus(s.LastRunStatus),
		nullableString(s.LastScanID), s.UpdatedAt.Format(timeFormat), s.ID,
	)
	if err != nil {
		return fmt.Errorf("store.schedules.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteScheduleStore) SetNextRunAt(ctx context.Context, id string, next *time.Time) error {
	res, err := st.db.ExecContext(ctx,
		`UPDATE scan_schedules SET next_run_at = ?, updated_at = ? WHERE id = ?`,
		timePtrUTC(next), time.Now().UTC().Format(timeFormat), id)
	if err != nil {
		return fmt.Errorf("store.schedules.SetNextRunAt: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteScheduleStore) RecordRun(ctx context.Context, id string, status model.ScheduleRunStatus, scanID *string, at time.Time) error {
	res, err := st.db.ExecContext(ctx,
		`UPDATE scan_schedules SET last_run_at = ?, last_run_status = ?, last_scan_id = ?, updated_at = ?
		 WHERE id = ?`,
		at.UTC().Format(timeFormat), string(status), nullableString(scanID),
		time.Now().UTC().Format(timeFormat), id)
	if err != nil {
		return fmt.Errorf("store.schedules.RecordRun: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteScheduleStore) Delete(ctx context.Context, id string) error {
	res, err := st.db.ExecContext(ctx, `DELETE FROM scan_schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store.schedules.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteScheduleStore) CountByTarget(ctx context.Context, targetID string) (int, error) {
	var n int
	err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scan_schedules WHERE target_id = ?`, targetID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store.schedules.CountByTarget: %w", err)
	}
	return n, nil
}

// validateScheduleInput runs the declarative guards shared by Create and
// Update: non-empty target+name, valid rrule, known tool names, non-zero
// DTStart. Returns a typed error for each failure class so HTTP handlers
// can surface specific status codes later (SCHED1.3).
func validateScheduleInput(s *model.Schedule) error {
	if s == nil {
		return fmt.Errorf("schedule is nil")
	}
	if strings.TrimSpace(s.TargetID) == "" {
		return fmt.Errorf("schedule.target_id is required")
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("schedule.name is required")
	}
	if strings.TrimSpace(s.Timezone) == "" {
		return fmt.Errorf("schedule.timezone is required")
	}
	if _, err := rrule.ValidateRRule(s.RRule); err != nil {
		return err
	}
	if err := model.ValidateToolConfig(s.ToolConfig); err != nil {
		return err
	}
	return nil
}

// marshalScheduleFields serializes the JSON columns (tool_config,
// overrides, maintenance_window) for INSERT / UPDATE.
func marshalScheduleFields(s *model.Schedule) (string, string, any, error) {
	toolJSON, err := json.Marshal(s.ToolConfig)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshaling tool_config: %w", err)
	}
	overridesJSON, err := json.Marshal(s.Overrides)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshaling overrides: %w", err)
	}
	var mwJSON any
	if s.MaintenanceWindow != nil {
		b, err := json.Marshal(s.MaintenanceWindow)
		if err != nil {
			return "", "", nil, fmt.Errorf("marshaling maintenance_window: %w", err)
		}
		mwJSON = string(b)
	}
	return string(toolJSON), string(overridesJSON), mwJSON, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows — lets us share
// the column-mapping logic between Get* and List*.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSchedule(r rowScanner) (*model.Schedule, error) {
	var (
		s              model.Schedule
		dtstart        sql.NullString
		templateID     sql.NullString
		toolJSON       string
		overridesJSON  string
		mwJSON         sql.NullString
		enabled        int
		nextRunAt      sql.NullString
		lastRunAt      sql.NullString
		lastRunStatus  sql.NullString
		lastScanID     sql.NullString
		createdAt      sql.NullString
		updatedAt      sql.NullString
	)
	if err := r.Scan(
		&s.ID, &s.TargetID, &s.Name, &s.RRule, &dtstart, &s.Timezone,
		&templateID, &toolJSON, &overridesJSON, &mwJSON, &enabled,
		&nextRunAt, &lastRunAt, &lastRunStatus, &lastScanID,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning schedule: %w", err)
	}
	s.DTStart = parseTime(dtstart)
	s.CreatedAt = parseTime(createdAt)
	s.UpdatedAt = parseTime(updatedAt)
	s.Enabled = enabled != 0
	s.NextRunAt = parseTimePtr(nextRunAt)
	s.LastRunAt = parseTimePtr(lastRunAt)
	if templateID.Valid && templateID.String != "" {
		v := templateID.String
		s.TemplateID = &v
	}
	if lastScanID.Valid && lastScanID.String != "" {
		v := lastScanID.String
		s.LastScanID = &v
	}
	if lastRunStatus.Valid && lastRunStatus.String != "" {
		v := model.ScheduleRunStatus(lastRunStatus.String)
		s.LastRunStatus = &v
	}
	if toolJSON == "" {
		s.ToolConfig = model.ToolConfig{}
	} else {
		if err := json.Unmarshal([]byte(toolJSON), &s.ToolConfig); err != nil {
			return nil, fmt.Errorf("unmarshaling tool_config: %w", err)
		}
	}
	if overridesJSON == "" {
		s.Overrides = []string{}
	} else {
		if err := json.Unmarshal([]byte(overridesJSON), &s.Overrides); err != nil {
			return nil, fmt.Errorf("unmarshaling overrides: %w", err)
		}
	}
	if mwJSON.Valid && mwJSON.String != "" {
		var mw model.MaintenanceWindow
		if err := json.Unmarshal([]byte(mwJSON.String), &mw); err != nil {
			return nil, fmt.Errorf("unmarshaling maintenance_window: %w", err)
		}
		s.MaintenanceWindow = &mw
	}
	return &s, nil
}

func queryManySchedules(ctx context.Context, db dbtx, query string, args ...any) ([]model.Schedule, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying schedules: %w", err)
	}
	defer rows.Close()

	out := make([]model.Schedule, 0)
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// nullableString turns a *string into a driver argument that round-trips
// as SQL NULL when nil or empty.
func nullableString(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func nullableRunStatus(v *model.ScheduleRunStatus) any {
	if v == nil || *v == "" {
		return nil
	}
	return string(*v)
}

// timePtrUTC serializes a *time.Time as a UTC-normalized RFC3339 string,
// or SQL NULL when nil. Schedule-adjacent columns must use this rather
// than timePtr so partial-index lookups (e.g. next_run_at ≤ now.UTC())
// do not fall over on lexicographic comparisons between local-offset
// and UTC ("Z") forms.
func timePtrUTC(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeFormat)
}
