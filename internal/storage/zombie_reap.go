package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// reapBatchSize bounds how many scan IDs are bound into a single SQLite
// statement. SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999; 500
// keeps comfortable headroom for the column placeholders in the same
// UPDATE. A healthy agent never has more than a handful of orphans;
// the chunking matters only on pathological crash-loop dbs.
const reapBatchSize = 500

// ReapTx is a transaction-bound view of the zombie-reap operations.
// Obtained via SQLiteStore.BeginReapTx; satisfies the intervalsched
// ZombieReapTx interface.
//
// Lifecycle: callers must call Commit or Rollback exactly once. Calling
// neither leaks the transaction.
type ReapTx struct {
	tx *sql.Tx
}

// BeginReapTx opens a transaction scoped to the zombie-reap operations.
// The returned *ReapTx exposes ListOrphanedScanIDs / MarkScansFailed /
// MarkAdHocRunsFailed / MarkToolRunsFailed bound to the transaction so
// every update inside the reap lands atomically.
func (s *SQLiteStore) BeginReapTx(ctx context.Context) (*ReapTx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin reap tx: %w", err)
	}
	return &ReapTx{tx: tx}, nil
}

// Commit finalizes the reap transaction.
func (r *ReapTx) Commit() error {
	return r.tx.Commit()
}

// Rollback aborts the reap transaction. Safe to call after Commit
// (returns sql.ErrTxDone, which the caller can ignore).
func (r *ReapTx) Rollback() error {
	return r.tx.Rollback()
}

// ListOrphanedScanIDs returns IDs of every scan with status='running'.
// Called once at scheduler startup; expected count is 0–N small.
func (r *ReapTx) ListOrphanedScanIDs(ctx context.Context) ([]string, error) {
	return listOrphanedScanIDs(ctx, r.tx)
}

// MarkScansFailed transitions all scans whose ID is in ids and whose
// status is still 'running' to status='failed' with the given error
// message and finished_at. Returns count of rows actually updated.
// Empty ids → (0, nil).
//
// IDs are processed in chunks of reapBatchSize to stay under SQLite's
// bound-parameter limit; the chunk loop runs inside the same tx so
// atomicity is preserved.
func (r *ReapTx) MarkScansFailed(ctx context.Context, ids []string, errMsg string, finishedAt time.Time) (int, error) {
	return markScansFailed(ctx, r.tx, ids, errMsg, finishedAt)
}

// MarkAdHocRunsFailed transitions ad_hoc_scan_runs rows whose scan_id is
// in scanIDs AND status IN ('pending','running') to status='failed'
// with completed_at=finishedAt. Empty scanIDs → (0, nil).
func (r *ReapTx) MarkAdHocRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	return markAdHocRunsFailed(ctx, r.tx, scanIDs, finishedAt)
}

// MarkToolRunsFailed transitions tool_runs rows whose scan_id is in
// scanIDs AND status='running' to status='failed' with
// finished_at=finishedAt and error_message='orphaned on scheduler
// restart'. Empty scanIDs → (0, nil).
func (r *ReapTx) MarkToolRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	return markToolRunsFailed(ctx, r.tx, scanIDs, finishedAt)
}

// ListOrphanedScanIDs is the non-tx variant exposed for tests and
// manual diagnostics. Production callers should use the BeginReapTx +
// ReapTx flow so the reap is atomic.
func (s *SQLiteStore) ListOrphanedScanIDs(ctx context.Context) ([]string, error) {
	return listOrphanedScanIDs(ctx, s.db)
}

// MarkScansFailed is the non-tx variant; see ReapTx.MarkScansFailed.
func (s *SQLiteStore) MarkScansFailed(ctx context.Context, ids []string, errMsg string, finishedAt time.Time) (int, error) {
	return markScansFailed(ctx, s.db, ids, errMsg, finishedAt)
}

// MarkAdHocRunsFailed is the non-tx variant; see ReapTx.MarkAdHocRunsFailed.
func (s *SQLiteStore) MarkAdHocRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	return markAdHocRunsFailed(ctx, s.db, scanIDs, finishedAt)
}

// MarkToolRunsFailed is the non-tx variant; see ReapTx.MarkToolRunsFailed.
func (s *SQLiteStore) MarkToolRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	return markToolRunsFailed(ctx, s.db, scanIDs, finishedAt)
}

func listOrphanedScanIDs(ctx context.Context, db dbtx) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM scans WHERE status = 'running'`)
	if err != nil {
		return nil, fmt.Errorf("listing orphaned scans: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning orphan row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating orphan rows: %w", err)
	}
	return ids, nil
}

func markScansFailed(ctx context.Context, db dbtx, ids []string, errMsg string, finishedAt time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(timeFormat)
	finished := finishedAt.UTC().Format(timeFormat)

	total := 0
	for _, chunk := range chunkIDs(ids, reapBatchSize) {
		placeholders, args := bindIDs(chunk)
		// status filter prevents double-reaping if this function is
		// called twice (idempotent contract).
		query := fmt.Sprintf(
			`UPDATE scans
			 SET status = 'failed', error = ?, finished_at = ?, updated_at = ?
			 WHERE status = 'running' AND id IN (%s)`,
			placeholders,
		)
		execArgs := make([]any, 0, len(args)+3)
		execArgs = append(execArgs, errMsg, finished, now)
		execArgs = append(execArgs, args...)
		res, err := db.ExecContext(ctx, query, execArgs...)
		if err != nil {
			return total, fmt.Errorf("marking scans failed: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

func markAdHocRunsFailed(ctx context.Context, db dbtx, scanIDs []string, finishedAt time.Time) (int, error) {
	if len(scanIDs) == 0 {
		return 0, nil
	}
	finished := finishedAt.UTC().Format(timeFormat)

	total := 0
	for _, chunk := range chunkIDs(scanIDs, reapBatchSize) {
		placeholders, args := bindIDs(chunk)
		query := fmt.Sprintf(
			`UPDATE ad_hoc_scan_runs
			 SET status = 'failed', completed_at = ?
			 WHERE status IN ('pending','running') AND scan_id IN (%s)`,
			placeholders,
		)
		execArgs := make([]any, 0, len(args)+1)
		execArgs = append(execArgs, finished)
		execArgs = append(execArgs, args...)
		res, err := db.ExecContext(ctx, query, execArgs...)
		if err != nil {
			return total, fmt.Errorf("marking ad-hoc runs failed: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

func markToolRunsFailed(ctx context.Context, db dbtx, scanIDs []string, finishedAt time.Time) (int, error) {
	if len(scanIDs) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(timeFormat)
	finished := finishedAt.UTC().Format(timeFormat)

	total := 0
	for _, chunk := range chunkIDs(scanIDs, reapBatchSize) {
		placeholders, args := bindIDs(chunk)
		query := fmt.Sprintf(
			`UPDATE tool_runs
			 SET status = 'failed',
			     finished_at = ?,
			     error_message = 'orphaned on scheduler restart',
			     updated_at = ?
			 WHERE status = 'running' AND scan_id IN (%s)`,
			placeholders,
		)
		execArgs := make([]any, 0, len(args)+2)
		execArgs = append(execArgs, finished, now)
		execArgs = append(execArgs, args...)
		res, err := db.ExecContext(ctx, query, execArgs...)
		if err != nil {
			return total, fmt.Errorf("marking tool runs failed: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

func chunkIDs(ids []string, size int) [][]string {
	if size <= 0 || len(ids) <= size {
		return [][]string{ids}
	}
	out := make([][]string, 0, (len(ids)+size-1)/size)
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

func bindIDs(ids []string) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = "?"
		args[i] = id
	}
	return strings.Join(parts, ","), args
}
