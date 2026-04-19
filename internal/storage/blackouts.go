package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
)

// BlackoutStore persists blackout windows (global or per-target). Used by
// the daemon's active-window evaluator, the HTTP handlers, and the CLI.
type BlackoutStore interface {
	Create(ctx context.Context, b *model.BlackoutWindow) error
	Get(ctx context.Context, id string) (*model.BlackoutWindow, error)
	List(ctx context.Context) ([]model.BlackoutWindow, error)
	ListByScope(ctx context.Context, scope model.BlackoutScope) ([]model.BlackoutWindow, error)
	ListByTarget(ctx context.Context, targetID string) ([]model.BlackoutWindow, error)
	ListActive(ctx context.Context, targetID string) ([]model.BlackoutWindow, error)
	Update(ctx context.Context, b *model.BlackoutWindow) error
	Delete(ctx context.Context, id string) error
}

// Blackouts returns a BlackoutStore backed by this SQLiteStore.
func (s *SQLiteStore) Blackouts() BlackoutStore {
	return &sqliteBlackoutStore{db: s.db}
}

type sqliteBlackoutStore struct {
	db *sql.DB
}

const blackoutColumns = `id, scope, target_id, name, rrule, duration_sec, timezone,
  enabled, created_at, updated_at`

func (st *sqliteBlackoutStore) Create(ctx context.Context, b *model.BlackoutWindow) error {
	if err := validateBlackoutInput(b); err != nil {
		return err
	}
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	b.CreatedAt = now
	b.UpdatedAt = now

	_, err := st.db.ExecContext(ctx,
		`INSERT INTO blackout_windows (`+blackoutColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, string(b.Scope), nullableString(b.TargetID), b.Name, b.RRule,
		b.DurationSec, b.Timezone, boolToInt(b.Enabled),
		b.CreatedAt.Format(timeFormat), b.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("store.blackouts.Create: %w", err)
	}
	return nil
}

func (st *sqliteBlackoutStore) Get(ctx context.Context, id string) (*model.BlackoutWindow, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+blackoutColumns+` FROM blackout_windows WHERE id = ?`, id)
	return scanBlackout(row)
}

func (st *sqliteBlackoutStore) List(ctx context.Context) ([]model.BlackoutWindow, error) {
	return queryManyBlackouts(ctx, st.db,
		`SELECT `+blackoutColumns+` FROM blackout_windows ORDER BY created_at ASC`)
}

func (st *sqliteBlackoutStore) ListByScope(ctx context.Context, scope model.BlackoutScope) ([]model.BlackoutWindow, error) {
	return queryManyBlackouts(ctx, st.db,
		`SELECT `+blackoutColumns+` FROM blackout_windows WHERE scope = ? ORDER BY created_at ASC`,
		string(scope))
}

func (st *sqliteBlackoutStore) ListByTarget(ctx context.Context, targetID string) ([]model.BlackoutWindow, error) {
	return queryManyBlackouts(ctx, st.db,
		`SELECT `+blackoutColumns+` FROM blackout_windows WHERE target_id = ? ORDER BY created_at ASC`,
		targetID)
}

// ListActive returns the enabled global blackouts plus the enabled
// target-scoped blackouts for the given target. Actual "is this window
// live now?" evaluation is performed by the scheduler from the RRULE —
// this store just hands over the candidate set.
func (st *sqliteBlackoutStore) ListActive(ctx context.Context, targetID string) ([]model.BlackoutWindow, error) {
	return queryManyBlackouts(ctx, st.db,
		`SELECT `+blackoutColumns+` FROM blackout_windows
		 WHERE enabled = 1 AND (scope = 'global' OR target_id = ?)
		 ORDER BY created_at ASC`,
		targetID)
}

func (st *sqliteBlackoutStore) Update(ctx context.Context, b *model.BlackoutWindow) error {
	if err := validateBlackoutInput(b); err != nil {
		return err
	}
	b.UpdatedAt = time.Now().UTC()

	res, err := st.db.ExecContext(ctx,
		`UPDATE blackout_windows SET
		   scope = ?, target_id = ?, name = ?, rrule = ?, duration_sec = ?,
		   timezone = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		string(b.Scope), nullableString(b.TargetID), b.Name, b.RRule,
		b.DurationSec, b.Timezone, boolToInt(b.Enabled),
		b.UpdatedAt.Format(timeFormat), b.ID,
	)
	if err != nil {
		return fmt.Errorf("store.blackouts.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteBlackoutStore) Delete(ctx context.Context, id string) error {
	res, err := st.db.ExecContext(ctx, `DELETE FROM blackout_windows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store.blackouts.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateBlackoutInput(b *model.BlackoutWindow) error {
	if b == nil {
		return fmt.Errorf("blackout is nil")
	}
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("blackout.name is required")
	}
	if b.DurationSec <= 0 {
		return fmt.Errorf("blackout.duration_seconds must be > 0")
	}
	if strings.TrimSpace(b.Timezone) == "" {
		return fmt.Errorf("blackout.timezone is required")
	}
	switch b.Scope {
	case model.BlackoutScopeGlobal:
		if b.TargetID != nil && *b.TargetID != "" {
			return fmt.Errorf("global blackout must not have target_id")
		}
	case model.BlackoutScopeTarget:
		if b.TargetID == nil || *b.TargetID == "" {
			return fmt.Errorf("target blackout must have target_id")
		}
	default:
		return fmt.Errorf("invalid blackout scope %q", b.Scope)
	}
	if _, err := rrule.ValidateRRule(b.RRule); err != nil {
		return err
	}
	return nil
}

func scanBlackout(r rowScanner) (*model.BlackoutWindow, error) {
	var (
		b         model.BlackoutWindow
		scope     string
		targetID  sql.NullString
		enabled   int
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	if err := r.Scan(
		&b.ID, &scope, &targetID, &b.Name, &b.RRule, &b.DurationSec, &b.Timezone,
		&enabled, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning blackout: %w", err)
	}
	b.Scope = model.BlackoutScope(scope)
	b.Enabled = enabled != 0
	b.CreatedAt = parseTime(createdAt)
	b.UpdatedAt = parseTime(updatedAt)
	if targetID.Valid && targetID.String != "" {
		v := targetID.String
		b.TargetID = &v
	}
	return &b, nil
}

func queryManyBlackouts(ctx context.Context, db *sql.DB, query string, args ...any) ([]model.BlackoutWindow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying blackouts: %w", err)
	}
	defer rows.Close()
	out := make([]model.BlackoutWindow, 0)
	for rows.Next() {
		b, err := scanBlackout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}
