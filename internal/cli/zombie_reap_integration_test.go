package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// TestZombieReap_FullPathThroughRealStore exercises the same call the
// runUI / runDaemonRun boot hooks make, against a real *storage.SQLiteStore.
// Verifies the adapter (NewZombieReapBackend) and the storage chunked
// updates land the canonical state on every cascade table.
func TestZombieReap_FullPathThroughRealStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reap.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()

	// Seed: one running scan with one running tool_run and one
	// pending ad_hoc_scan_runs row (the typical orphaned trio).
	target := &model.Target{Value: "reap-it.example.com"}
	require.NoError(t, store.CreateTarget(ctx, target))

	scan := &model.Scan{
		TargetID: target.ID,
		Type:     model.ScanTypeFull,
		Status:   model.ScanStatusRunning,
	}
	require.NoError(t, store.CreateScan(ctx, scan))

	tr := &model.ToolRun{
		ScanID:   scan.ID,
		ToolName: "subfinder",
		Phase:    "discovery",
		Status:   model.ToolRunRunning,
	}
	require.NoError(t, store.CreateToolRun(ctx, tr))

	adhoc := &model.AdHocScanRun{
		TargetID:    target.ID,
		InitiatedBy: "user:test",
		ScanID:      &scan.ID,
		Status:      model.AdHocPending,
	}
	require.NoError(t, store.AdHocScanRuns().Create(ctx, adhoc))

	// Survivor scan + tool_run that must NOT be touched.
	survivor := &model.Scan{
		TargetID: target.ID,
		Type:     model.ScanTypeFull,
		Status:   model.ScanStatusCompleted,
	}
	require.NoError(t, store.CreateScan(ctx, survivor))

	survivorTR := &model.ToolRun{
		ScanID:   survivor.ID,
		ToolName: "httpx",
		Phase:    "http_probe",
		Status:   model.ToolRunCompleted,
	}
	require.NoError(t, store.CreateToolRun(ctx, survivorTR))

	// Drive the production reap call — same path the boot hooks use.
	report, err := intervalsched.ReapOrphanedScans(
		ctx,
		intervalsched.NewZombieReapBackend(store),
		intervalsched.NewRealClock(),
		slog.Default(),
	)
	require.NoError(t, err)

	assert.Equal(t, 1, report.ScansReaped)
	assert.Equal(t, 1, report.AdHocRunsReaped)
	assert.Equal(t, 1, report.ToolRunsReaped)
	assert.True(t, report.Duration > 0)

	// Orphan scan: 'running' → 'failed' with canonical error and finished_at.
	reaped, err := store.GetScan(ctx, scan.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusFailed, reaped.Status)
	assert.Equal(t, "orphaned on scheduler restart", reaped.Error)
	require.NotNil(t, reaped.FinishedAt)

	// Orphan tool_run: status must flip and ListToolRuns surfaces it.
	toolRuns, err := store.ListToolRuns(ctx, scan.ID)
	require.NoError(t, err)
	require.Len(t, toolRuns, 1)
	assert.Equal(t, model.ToolRunFailed, toolRuns[0].Status)
	assert.Equal(t, "orphaned on scheduler restart", toolRuns[0].ErrorMessage)

	// Orphan ad-hoc run.
	gotAdhoc, err := store.AdHocScanRuns().Get(ctx, adhoc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AdHocFailed, gotAdhoc.Status)

	// Survivor scan + tool_run must be untouched.
	preservedScan, err := store.GetScan(ctx, survivor.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusCompleted, preservedScan.Status)

	preservedTRs, err := store.ListToolRuns(ctx, survivor.ID)
	require.NoError(t, err)
	require.Len(t, preservedTRs, 1)
	assert.Equal(t, model.ToolRunCompleted, preservedTRs[0].Status)
}

// TestZombieReap_Idempotent verifies a second call against the same DB
// returns a zero report — the production hook can fire safely on every
// boot without re-touching already-reaped rows.
func TestZombieReap_Idempotent(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reap.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()

	target := &model.Target{Value: "double-reap.example.com"}
	require.NoError(t, store.CreateTarget(ctx, target))
	scan := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, store.CreateScan(ctx, scan))

	backend := intervalsched.NewZombieReapBackend(store)
	clk := intervalsched.NewRealClock()

	r1, err := intervalsched.ReapOrphanedScans(ctx, backend, clk, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, 1, r1.ScansReaped)

	r2, err := intervalsched.ReapOrphanedScans(ctx, backend, clk, slog.Default())
	require.NoError(t, err)
	assert.Zero(t, r2.ScansReaped, "second pass must be a no-op")
}
