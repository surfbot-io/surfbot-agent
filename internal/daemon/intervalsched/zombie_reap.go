package intervalsched

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// orphanedScanError is the canonical error string written to scans.error
// (and tool_runs.error_message) when a scan is reaped on restart.
// Single, grep-able phrase across the codebase.
const orphanedScanError = "orphaned on scheduler restart"

// reapWarnThreshold is the count of orphan scans above which we log a
// warning. A healthy agent never has more than a handful; tens of
// thousands suggest a crash loop or a much bigger problem upstream.
const reapWarnThreshold = 10000

// ReapReport summarizes what ReapOrphanedScans did. Empty zero-value
// means there were no orphans.
type ReapReport struct {
	ScansReaped     int
	AdHocRunsReaped int
	ToolRunsReaped  int
	Duration        time.Duration
}

// ZombieReapBackend is the storage slice ReapOrphanedScans needs.
// Wrap the production *storage.SQLiteStore via NewZombieReapBackend;
// tests inject a fake.
type ZombieReapBackend interface {
	BeginReapTx(ctx context.Context) (ZombieReapTx, error)
}

// NewZombieReapBackend adapts a *storage.SQLiteStore to the
// ZombieReapBackend interface. The adapter is required because
// SQLiteStore.BeginReapTx returns the concrete *storage.ReapTx (which
// structurally satisfies ZombieReapTx) — Go's interface-vs-concrete
// return-type rules mean *SQLiteStore does not directly satisfy
// ZombieReapBackend without this one-line bridge.
func NewZombieReapBackend(s *storage.SQLiteStore) ZombieReapBackend {
	return reapBackendAdapter{s: s}
}

type reapBackendAdapter struct {
	s *storage.SQLiteStore
}

func (a reapBackendAdapter) BeginReapTx(ctx context.Context) (ZombieReapTx, error) {
	return a.s.BeginReapTx(ctx)
}

// ZombieReapTx is the transaction-bound surface the reap function
// drives. Implementations must commit only on Commit() and discard
// every write on Rollback().
type ZombieReapTx interface {
	ListOrphanedScanIDs(ctx context.Context) ([]string, error)
	MarkScansFailed(ctx context.Context, ids []string, errMsg string, finishedAt time.Time) (int, error)
	MarkAdHocRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error)
	MarkToolRunsFailed(ctx context.Context, scanIDs []string, finishedAt time.Time) (int, error)
	Commit() error
	Rollback() error
}

// ReapOrphanedScans atomically marks every scan whose status is
// 'running' as 'failed' with the canonical "orphaned on scheduler
// restart" message, sets finished_at, and cascades the reap to
// ad_hoc_scan_runs (pending|running) and tool_runs (running) that
// reference those scans.
//
// Intended to be called exactly once at scheduler startup, AFTER the
// scheduler_lock has been acquired and BEFORE the master ticker
// starts. Safe to call when there are no orphans (no-op).
//
// On any storage error the entire transaction is rolled back and the
// error is returned — partial reap is never persisted.
//
// Note on graceful shutdown: per the SCHED2.0 R3/R4 contract, when the
// scheduler exits normally, in-flight scans observe context
// cancellation and persist as status='cancelled' before the process
// terminates (see internal/pipeline/pipeline.go). Reap therefore only
// catches genuine crashes (panic, OOM, kill -9, power loss) and the
// edge case where shutdown grace expired with scans still running —
// both of which are correctly classified as 'failed' here.
func ReapOrphanedScans(
	ctx context.Context,
	store ZombieReapBackend,
	clock Clock,
	logger *slog.Logger,
) (ReapReport, error) {
	if logger == nil {
		logger = slog.Default()
	}
	start := clock.Now()

	tx, err := store.BeginReapTx(ctx)
	if err != nil {
		return ReapReport{}, fmt.Errorf("begin reap tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	ids, err := tx.ListOrphanedScanIDs(ctx)
	if err != nil {
		return ReapReport{}, fmt.Errorf("listing orphans: %w", err)
	}
	if len(ids) == 0 {
		// Empty case: commit the (no-op) tx so we don't leak it. No
		// log lines above DEBUG — silent on the happy path.
		if err := tx.Commit(); err != nil {
			return ReapReport{}, fmt.Errorf("committing empty reap: %w", err)
		}
		committed = true
		return ReapReport{Duration: clock.Now().Sub(start)}, nil
	}

	if len(ids) > reapWarnThreshold {
		logger.Warn("zombie reap: orphan count is unusually high — possible crash loop",
			"count", len(ids), "threshold", reapWarnThreshold)
	}
	logger.Info("reaping orphaned scans", "count", len(ids))

	finishedAt := clock.Now().UTC()

	scansN, err := tx.MarkScansFailed(ctx, ids, orphanedScanError, finishedAt)
	if err != nil {
		return ReapReport{}, fmt.Errorf("marking scans failed: %w", err)
	}
	adhocN, err := tx.MarkAdHocRunsFailed(ctx, ids, finishedAt)
	if err != nil {
		return ReapReport{}, fmt.Errorf("marking ad-hoc runs failed: %w", err)
	}
	toolsN, err := tx.MarkToolRunsFailed(ctx, ids, finishedAt)
	if err != nil {
		return ReapReport{}, fmt.Errorf("marking tool runs failed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ReapReport{}, fmt.Errorf("committing reap: %w", err)
	}
	committed = true

	for _, id := range ids {
		logger.Info("scan reaped", "scan_id", id)
	}

	report := ReapReport{
		ScansReaped:     scansN,
		AdHocRunsReaped: adhocN,
		ToolRunsReaped:  toolsN,
		Duration:        clock.Now().Sub(start),
	}
	logger.Info("zombie reap complete",
		"scans", report.ScansReaped,
		"adhoc_runs", report.AdHocRunsReaped,
		"tool_runs", report.ToolRunsReaped,
		"duration_ms", report.Duration.Milliseconds(),
	)
	return report, nil
}
