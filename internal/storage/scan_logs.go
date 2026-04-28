package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// InsertScanLogs writes a batch of log lines in a single transaction. The
// SQLiteLogSink calls this from its background goroutine; callers
// outside the sink path are unusual.
//
// Each row is inserted via a single multi-row VALUES statement so the
// per-batch overhead is one round-trip and one fsync (with WAL). The
// trade-off vs. a prepared statement re-used per row is measured at
// 50–100 lines per batch; for surfbot's typical scan rate that's
// dramatically faster.
func (s *SQLiteStore) InsertScanLogs(ctx context.Context, logs []model.ScanLog) error {
	if len(logs) == 0 {
		return nil
	}
	const cols = "(scan_id, tool_run_id, ts, source, level, text, created_at)"
	placeholders := make([]string, len(logs))
	args := make([]any, 0, len(logs)*7)
	for i, l := range logs {
		placeholders[i] = "(?, ?, ?, ?, ?, ?, ?)"
		var trID any
		if l.ToolRunID != "" {
			trID = l.ToolRunID
		}
		level := string(l.Level)
		if level == "" {
			level = string(model.LogLevelInfo)
		}
		args = append(args,
			l.ScanID,
			trID,
			l.Timestamp.UnixMilli(),
			l.Source,
			level,
			l.Text,
			l.CreatedAt.UTC().Format(time.RFC3339Nano),
		)
	}
	q := "INSERT INTO scan_logs " + cols + " VALUES " + strings.Join(placeholders, ", ")
	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("insert scan_logs: %w", err)
	}
	return nil
}

// ListScanLogs returns log lines for the given scan, ordered by id ASC
// (chronological). Pagination is via the Since cursor (exclusive lower
// bound on id). The default limit is 200; callers may pass any value —
// the HTTP handler caps at 1000.
func (s *SQLiteStore) ListScanLogs(ctx context.Context, opts ScanLogListOptions) ([]model.ScanLog, error) {
	if opts.ScanID == "" {
		return nil, fmt.Errorf("scan_id required")
	}
	if opts.Limit <= 0 {
		opts.Limit = 200
	}

	conds := []string{"scan_id = ?"}
	args := []any{opts.ScanID}
	if opts.Since > 0 {
		conds = append(conds, "id > ?")
		args = append(args, opts.Since)
	}
	if len(opts.Level) > 0 {
		ph := make([]string, len(opts.Level))
		for i, lv := range opts.Level {
			ph[i] = "?"
			args = append(args, string(lv))
		}
		conds = append(conds, "level IN ("+strings.Join(ph, ",")+")")
	}
	if len(opts.Source) > 0 {
		ph := make([]string, len(opts.Source))
		for i, src := range opts.Source {
			ph[i] = "?"
			args = append(args, src)
		}
		conds = append(conds, "source IN ("+strings.Join(ph, ",")+")")
	}
	if opts.ToolRunID != "" {
		conds = append(conds, "tool_run_id = ?")
		args = append(args, opts.ToolRunID)
	}
	q := `SELECT id, scan_id, IFNULL(tool_run_id, ''), ts, source, level, text, created_at
	      FROM scan_logs WHERE ` + strings.Join(conds, " AND ") + `
	      ORDER BY id ASC LIMIT ?`
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query scan_logs: %w", err)
	}
	defer rows.Close()
	out := make([]model.ScanLog, 0, opts.Limit)
	for rows.Next() {
		var l model.ScanLog
		var tsMs int64
		var createdAt string
		var lvl string
		if err := rows.Scan(&l.ID, &l.ScanID, &l.ToolRunID, &tsMs, &l.Source, &lvl, &l.Text, &createdAt); err != nil {
			return nil, fmt.Errorf("scan scan_log row: %w", err)
		}
		l.Timestamp = time.UnixMilli(tsMs).UTC()
		l.Level = model.LogLevel(lvl)
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			l.CreatedAt = t
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CountScanLogs returns the total log line count for a scan. No filters —
// the UI uses this for the "X total" badge so it sees the absolute total
// regardless of any filter chips applied.
func (s *SQLiteStore) CountScanLogs(ctx context.Context, scanID string) (int, error) {
	if scanID == "" {
		return 0, fmt.Errorf("scan_id required")
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM scan_logs WHERE scan_id = ?`, scanID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// PruneScanLogsOlderThan deletes rows whose created_at is strictly
// older than cutoff. Returns rows affected. The retention sweeper calls
// this once a day when a retention setting is configured; absent the
// setting, this is never invoked.
func (s *SQLiteStore) PruneScanLogsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM scan_logs WHERE created_at < ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune scan_logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
