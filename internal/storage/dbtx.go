package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// dbtx is the common read/write surface satisfied by both *sql.DB and
// *sql.Tx. Store implementations depend on this so a single concrete
// store can execute either against the outer connection pool or inside
// a caller-supplied transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// TxStores bundles the schedule-adjacent stores rebound against a single
// transaction. MigrateLegacyScheduleConfig (and future multi-store
// operations) receive one of these from SQLiteStore.Transact so every
// write lands atomically.
type TxStores struct {
	Schedules        ScheduleStore
	Templates        TemplateStore
	Blackouts        BlackoutStore
	ScheduleDefaults ScheduleDefaultsStore
	AdHocScanRuns    AdHocScanRunStore
}

// Transact runs fn inside a database transaction. If fn returns an
// error, the transaction is rolled back; otherwise it is committed. The
// TxStores argument exposes every schedule-adjacent store rebound to
// the tx so the fn body can perform multi-store writes atomically.
func (s *SQLiteStore) Transact(ctx context.Context, fn func(context.Context, TxStores) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage.Transact: begin: %w", err)
	}
	stores := TxStores{
		Schedules:        &sqliteScheduleStore{db: tx},
		Templates:        &sqliteTemplateStore{db: tx},
		Blackouts:        &sqliteBlackoutStore{db: tx},
		ScheduleDefaults: &sqliteScheduleDefaultsStore{db: tx},
		AdHocScanRuns:    &sqliteAdHocScanRunStore{db: tx},
	}
	if err := fn(ctx, stores); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("storage.Transact: %v; rollback: %v", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage.Transact: commit: %w", err)
	}
	return nil
}
