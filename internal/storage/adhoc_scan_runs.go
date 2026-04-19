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
)

// AdHocScanRunStore persists one-off scan invocations (CLI, web UI, API,
// run-once). Distinct from schedule dispatches — ad-hoc runs have no
// RRULE, a required `initiated_by` attribution, and flow through
// independently of the ticker.
type AdHocScanRunStore interface {
	Create(ctx context.Context, r *model.AdHocScanRun) error
	Get(ctx context.Context, id string) (*model.AdHocScanRun, error)
	ListByTarget(ctx context.Context, targetID string, limit int) ([]model.AdHocScanRun, error)
	ListByStatus(ctx context.Context, status model.AdHocRunStatus) ([]model.AdHocScanRun, error)
	UpdateStatus(ctx context.Context, id string, status model.AdHocRunStatus, at time.Time) error
	AttachScan(ctx context.Context, id, scanID string) error
	Delete(ctx context.Context, id string) error
}

// AdHocScanRuns returns an AdHocScanRunStore backed by this SQLiteStore.
func (s *SQLiteStore) AdHocScanRuns() AdHocScanRunStore {
	return &sqliteAdHocScanRunStore{db: s.db}
}

type sqliteAdHocScanRunStore struct {
	db *sql.DB
}

const adhocColumns = `id, target_id, template_id, tool_config, initiated_by, reason,
  scan_id, status, requested_at, started_at, completed_at`

func (st *sqliteAdHocScanRunStore) Create(ctx context.Context, r *model.AdHocScanRun) error {
	if err := validateAdHocInput(r); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = time.Now().UTC()
	}
	if r.ToolConfig == nil {
		r.ToolConfig = model.ToolConfig{}
	}

	toolJSON, err := json.Marshal(r.ToolConfig)
	if err != nil {
		return fmt.Errorf("marshaling tool_config: %w", err)
	}

	_, err = st.db.ExecContext(ctx,
		`INSERT INTO ad_hoc_scan_runs (`+adhocColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TargetID, nullableString(r.TemplateID), string(toolJSON),
		r.InitiatedBy, r.Reason, nullableString(r.ScanID), string(r.Status),
		r.RequestedAt.UTC().Format(timeFormat),
		timePtrUTC(r.StartedAt), timePtrUTC(r.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("store.adhoc.Create: %w", err)
	}
	return nil
}

func (st *sqliteAdHocScanRunStore) Get(ctx context.Context, id string) (*model.AdHocScanRun, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+adhocColumns+` FROM ad_hoc_scan_runs WHERE id = ?`, id)
	return scanAdHoc(row)
}

func (st *sqliteAdHocScanRunStore) ListByTarget(ctx context.Context, targetID string, limit int) ([]model.AdHocScanRun, error) {
	if limit <= 0 {
		limit = 100
	}
	return queryManyAdHoc(ctx, st.db,
		`SELECT `+adhocColumns+` FROM ad_hoc_scan_runs
		 WHERE target_id = ? ORDER BY requested_at DESC LIMIT ?`,
		targetID, limit)
}

func (st *sqliteAdHocScanRunStore) ListByStatus(ctx context.Context, status model.AdHocRunStatus) ([]model.AdHocScanRun, error) {
	return queryManyAdHoc(ctx, st.db,
		`SELECT `+adhocColumns+` FROM ad_hoc_scan_runs
		 WHERE status = ? ORDER BY requested_at ASC`,
		string(status))
}

func (st *sqliteAdHocScanRunStore) UpdateStatus(ctx context.Context, id string, status model.AdHocRunStatus, at time.Time) error {
	// Mirror the status onto started_at / completed_at where the
	// transition warrants it. A `running` transition pins started_at;
	// `completed`, `failed`, `cancelled` pin completed_at.
	var startedCol, completedCol any
	stamp := at.UTC().Format(timeFormat)
	switch status {
	case model.AdHocRunning:
		startedCol = stamp
	case model.AdHocCompleted, model.AdHocFailed, model.AdHocCancelled:
		completedCol = stamp
	}

	query := `UPDATE ad_hoc_scan_runs SET status = ?`
	args := []any{string(status)}
	if startedCol != nil {
		query += `, started_at = COALESCE(started_at, ?)`
		args = append(args, startedCol)
	}
	if completedCol != nil {
		query += `, completed_at = ?`
		args = append(args, completedCol)
	}
	query += ` WHERE id = ?`
	args = append(args, id)

	res, err := st.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store.adhoc.UpdateStatus: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteAdHocScanRunStore) AttachScan(ctx context.Context, id, scanID string) error {
	res, err := st.db.ExecContext(ctx,
		`UPDATE ad_hoc_scan_runs SET scan_id = ? WHERE id = ?`, scanID, id)
	if err != nil {
		return fmt.Errorf("store.adhoc.AttachScan: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteAdHocScanRunStore) Delete(ctx context.Context, id string) error {
	res, err := st.db.ExecContext(ctx, `DELETE FROM ad_hoc_scan_runs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store.adhoc.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateAdHocInput(r *model.AdHocScanRun) error {
	if r == nil {
		return fmt.Errorf("ad-hoc run is nil")
	}
	if strings.TrimSpace(r.TargetID) == "" {
		return fmt.Errorf("ad_hoc.target_id is required")
	}
	if strings.TrimSpace(r.InitiatedBy) == "" {
		return fmt.Errorf("ad_hoc.initiated_by is required")
	}
	switch r.Status {
	case "":
		r.Status = model.AdHocPending
	case model.AdHocPending, model.AdHocRunning, model.AdHocCompleted, model.AdHocFailed, model.AdHocCancelled:
	default:
		return fmt.Errorf("invalid ad_hoc.status %q", r.Status)
	}
	if err := model.ValidateToolConfig(r.ToolConfig); err != nil {
		return err
	}
	return nil
}

func scanAdHoc(r rowScanner) (*model.AdHocScanRun, error) {
	var (
		ah           model.AdHocScanRun
		templateID   sql.NullString
		toolJSON     string
		scanID       sql.NullString
		status       string
		requestedAt  sql.NullString
		startedAt    sql.NullString
		completedAt  sql.NullString
	)
	if err := r.Scan(
		&ah.ID, &ah.TargetID, &templateID, &toolJSON, &ah.InitiatedBy, &ah.Reason,
		&scanID, &status, &requestedAt, &startedAt, &completedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning ad_hoc_scan_run: %w", err)
	}
	ah.Status = model.AdHocRunStatus(status)
	ah.RequestedAt = parseTime(requestedAt)
	ah.StartedAt = parseTimePtr(startedAt)
	ah.CompletedAt = parseTimePtr(completedAt)
	if templateID.Valid && templateID.String != "" {
		v := templateID.String
		ah.TemplateID = &v
	}
	if scanID.Valid && scanID.String != "" {
		v := scanID.String
		ah.ScanID = &v
	}
	if toolJSON == "" {
		ah.ToolConfig = model.ToolConfig{}
	} else {
		if err := json.Unmarshal([]byte(toolJSON), &ah.ToolConfig); err != nil {
			return nil, fmt.Errorf("unmarshaling ad_hoc.tool_config: %w", err)
		}
	}
	return &ah, nil
}

func queryManyAdHoc(ctx context.Context, db *sql.DB, query string, args ...any) ([]model.AdHocScanRun, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying ad_hoc_scan_runs: %w", err)
	}
	defer rows.Close()
	out := make([]model.AdHocScanRun, 0)
	for rows.Next() {
		r, err := scanAdHoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
