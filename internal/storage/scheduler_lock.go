package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrLockHeld is returned by AcquireSchedulerLock when another live
// process currently owns the scheduler lock. The returned SchedulerLock
// record describes the holder so callers can render a useful message
// (pid, hostname, how long ago it was acquired).
var ErrLockHeld = errors.New("scheduler_lock held by another process")

// SchedulerLock mirrors the scheduler_lock row. It is emitted with
// ErrLockHeld when acquisition fails so the caller can surface the
// holder's identity.
type SchedulerLock struct {
	PID         int
	Hostname    string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
}

// Age returns how long ago the lock last heartbeated. Callers use this
// plus the configured heartbeat interval to decide whether the lock is
// stale and safe to reclaim.
func (l SchedulerLock) Age(now time.Time) time.Duration {
	if l.HeartbeatAt.IsZero() {
		return 0
	}
	return now.Sub(l.HeartbeatAt)
}

// AcquireSchedulerLock takes exclusive ownership of the scheduler slot
// for (pid, hostname). Three outcomes:
//
//   - No row exists → insert ours, return the lock.
//   - Row exists but heartbeat is older than staleAfter → our caller
//     has decided the holder is dead (or the same-host PID check has
//     already confirmed the process is gone); we overwrite the row.
//   - Row exists and is fresh → return ErrLockHeld with the holder.
//
// The caller is responsible for the is-PID-alive check on same-host
// conflicts; this store treats staleAfter as the single stalemate-breaker.
func (s *SQLiteStore) AcquireSchedulerLock(ctx context.Context, pid int, hostname string, staleAfter time.Duration) (*SchedulerLock, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		curPID      sql.NullInt64
		curHost     sql.NullString
		curAcquired sql.NullString
		curBeat     sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT pid, hostname, acquired_at, heartbeat_at FROM scheduler_lock WHERE id = 1`,
	).Scan(&curPID, &curHost, &curAcquired, &curBeat)

	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("reading scheduler_lock: %w", err)
	}

	if existing {
		heartbeat, _ := time.Parse(timeFormat, nullString(curBeat))
		acquiredAt, _ := time.Parse(timeFormat, nullString(curAcquired))
		held := SchedulerLock{
			PID:         int(curPID.Int64),
			Hostname:    nullString(curHost),
			AcquiredAt:  acquiredAt,
			HeartbeatAt: heartbeat,
		}
		if !heartbeat.IsZero() && now.Sub(heartbeat) <= staleAfter {
			return &held, fmt.Errorf("%w: pid=%d host=%s", ErrLockHeld, held.PID, held.Hostname)
		}
		// Stale: overwrite.
		if _, err := tx.ExecContext(ctx,
			`UPDATE scheduler_lock SET pid = ?, hostname = ?, acquired_at = ?, heartbeat_at = ? WHERE id = 1`,
			pid, hostname, now.Format(timeFormat), now.Format(timeFormat),
		); err != nil {
			return nil, fmt.Errorf("overwriting stale lock: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO scheduler_lock (id, pid, hostname, acquired_at, heartbeat_at) VALUES (1, ?, ?, ?, ?)`,
			pid, hostname, now.Format(timeFormat), now.Format(timeFormat),
		); err != nil {
			return nil, fmt.Errorf("inserting scheduler_lock: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scheduler_lock: %w", err)
	}
	return &SchedulerLock{
		PID:         pid,
		Hostname:    hostname,
		AcquiredAt:  now,
		HeartbeatAt: now,
	}, nil
}

// RefreshSchedulerLock bumps heartbeat_at iff the current holder still
// matches (pid, hostname). Called periodically while the scheduler is
// running so a takeover by a second process only happens after the
// holder has actually gone away.
func (s *SQLiteStore) RefreshSchedulerLock(ctx context.Context, pid int, hostname string) error {
	now := time.Now().UTC().Format(timeFormat)
	res, err := s.db.ExecContext(ctx,
		`UPDATE scheduler_lock SET heartbeat_at = ? WHERE id = 1 AND pid = ? AND hostname = ?`,
		now, pid, hostname,
	)
	if err != nil {
		return fmt.Errorf("refreshing scheduler_lock: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler_lock no longer owned by pid=%d host=%s", pid, hostname)
	}
	return nil
}

// ReleaseSchedulerLock deletes the lock row iff (pid, hostname) still
// owns it. Safe to call during shutdown; a missed release is harmless
// because the next acquirer will reclaim via the staleAfter path.
func (s *SQLiteStore) ReleaseSchedulerLock(ctx context.Context, pid int, hostname string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM scheduler_lock WHERE id = 1 AND pid = ? AND hostname = ?`,
		pid, hostname,
	)
	if err != nil {
		return fmt.Errorf("releasing scheduler_lock: %w", err)
	}
	return nil
}

func nullString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}
