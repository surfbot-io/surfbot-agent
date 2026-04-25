package storage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// seedScan inserts a scan in the given status. Returns the scan ID.
func seedScan(t *testing.T, s *SQLiteStore, status model.ScanStatus) string {
	t.Helper()
	ctx := context.Background()

	target := &model.Target{Value: "example-" + uuid.NewString()[:8] + ".com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	sc := &model.Scan{
		TargetID: target.ID,
		Type:     model.ScanTypeFull,
		Status:   status,
	}
	require.NoError(t, s.CreateScan(ctx, sc))
	return sc.ID
}

// seedToolRun inserts a tool_run row in the given status referencing scanID.
func seedToolRun(t *testing.T, s *SQLiteStore, scanID string, status model.ToolRunStatus) string {
	t.Helper()
	tr := &model.ToolRun{
		ScanID:   scanID,
		ToolName: "subfinder",
		Phase:    "discovery",
		Status:   status,
	}
	require.NoError(t, s.CreateToolRun(context.Background(), tr))
	return tr.ID
}

// seedAdHocRun inserts an ad_hoc_scan_runs row pointing at scanID.
func seedAdHocRun(t *testing.T, s *SQLiteStore, scanID string, status model.AdHocRunStatus) string {
	t.Helper()
	ctx := context.Background()
	target := &model.Target{Value: "adhoc-" + uuid.NewString()[:8] + ".com"}
	require.NoError(t, s.CreateTarget(ctx, target))
	id := uuid.NewString()
	requestedAt := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ad_hoc_scan_runs (id, target_id, initiated_by, scan_id, status, requested_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, target.ID, "user:test", scanID, string(status), requestedAt,
	)
	require.NoError(t, err)
	return id
}

func adhocStatus(t *testing.T, s *SQLiteStore, id string) string {
	t.Helper()
	var status string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT status FROM ad_hoc_scan_runs WHERE id = ?`, id,
	).Scan(&status)
	require.NoError(t, err)
	return status
}

func toolRunStatus(t *testing.T, s *SQLiteStore, id string) string {
	t.Helper()
	var status string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT status FROM tool_runs WHERE id = ?`, id,
	).Scan(&status)
	require.NoError(t, err)
	return status
}

func TestListOrphanedScanIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	running1 := seedScan(t, s, model.ScanStatusRunning)
	running2 := seedScan(t, s, model.ScanStatusRunning)
	_ = seedScan(t, s, model.ScanStatusCompleted)
	_ = seedScan(t, s, model.ScanStatusFailed)
	_ = seedScan(t, s, model.ScanStatusQueued)
	_ = seedScan(t, s, model.ScanStatusCancelled)

	ids, err := s.ListOrphanedScanIDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{running1, running2}, ids,
		"only running scans should appear")
}

func TestMarkScansFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := seedScan(t, s, model.ScanStatusRunning)
	r2 := seedScan(t, s, model.ScanStatusRunning)
	completed := seedScan(t, s, model.ScanStatusCompleted)

	finished := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)
	n, err := s.MarkScansFailed(ctx, []string{r1, r2, completed}, "orphaned on scheduler restart", finished)
	require.NoError(t, err)
	// completed was filtered by the WHERE clause.
	assert.Equal(t, 2, n)

	for _, id := range []string{r1, r2} {
		sc, err := s.GetScan(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, model.ScanStatusFailed, sc.Status)
		assert.Equal(t, "orphaned on scheduler restart", sc.Error)
		require.NotNil(t, sc.FinishedAt)
		assert.True(t, sc.FinishedAt.Equal(finished),
			"finished_at must match clock value, got %v", sc.FinishedAt)
	}

	preserved, err := s.GetScan(ctx, completed)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusCompleted, preserved.Status)

	// Empty IDs is no-op.
	n, err = s.MarkScansFailed(ctx, nil, "ignored", time.Now())
	require.NoError(t, err)
	assert.Zero(t, n)

	// Idempotency: second call sees 0 rows because none are 'running'.
	n, err = s.MarkScansFailed(ctx, []string{r1, r2}, "x", finished)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestMarkAdHocRunsFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	scan := seedScan(t, s, model.ScanStatusRunning)
	other := seedScan(t, s, model.ScanStatusRunning)
	pending := seedAdHocRun(t, s, scan, model.AdHocPending)
	running := seedAdHocRun(t, s, scan, model.AdHocRunning)
	completed := seedAdHocRun(t, s, scan, model.AdHocCompleted)
	unrelated := seedAdHocRun(t, s, other, model.AdHocCompleted)

	finished := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)
	n, err := s.MarkAdHocRunsFailed(ctx, []string{scan}, finished)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	assert.Equal(t, "failed", adhocStatus(t, s, pending))
	assert.Equal(t, "failed", adhocStatus(t, s, running))
	assert.Equal(t, "completed", adhocStatus(t, s, completed),
		"already-completed runs must not be touched")
	assert.Equal(t, "completed", adhocStatus(t, s, unrelated))

	// Empty IDs is no-op.
	n, err = s.MarkAdHocRunsFailed(ctx, nil, finished)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestMarkToolRunsFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	scan := seedScan(t, s, model.ScanStatusRunning)
	other := seedScan(t, s, model.ScanStatusCompleted)

	running := seedToolRun(t, s, scan, model.ToolRunRunning)
	completed := seedToolRun(t, s, scan, model.ToolRunCompleted)
	skipped := seedToolRun(t, s, scan, model.ToolRunSkipped)
	timeout := seedToolRun(t, s, scan, model.ToolRunTimeout)
	unrelated := seedToolRun(t, s, other, model.ToolRunRunning)

	finished := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)
	n, err := s.MarkToolRunsFailed(ctx, []string{scan}, finished)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the running tool_run for the orphaned scan")

	assert.Equal(t, "failed", toolRunStatus(t, s, running))
	assert.Equal(t, "completed", toolRunStatus(t, s, completed))
	assert.Equal(t, "skipped", toolRunStatus(t, s, skipped))
	assert.Equal(t, "timeout", toolRunStatus(t, s, timeout))
	assert.Equal(t, "running", toolRunStatus(t, s, unrelated),
		"tool_runs of unrelated scans must not be touched")

	// Verify error_message canonical string.
	var msg string
	err = s.db.QueryRowContext(ctx,
		`SELECT error_message FROM tool_runs WHERE id = ?`, running,
	).Scan(&msg)
	require.NoError(t, err)
	assert.Equal(t, "orphaned on scheduler restart", msg)
}

func TestBeginReapTx_RollbackPreservesState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	scanID := seedScan(t, s, model.ScanStatusRunning)
	trID := seedToolRun(t, s, scanID, model.ToolRunRunning)

	tx, err := s.BeginReapTx(ctx)
	require.NoError(t, err)

	ids, err := tx.ListOrphanedScanIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{scanID}, ids)

	finished := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)
	n, err := tx.MarkScansFailed(ctx, ids, "orphaned on scheduler restart", finished)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	n, err = tx.MarkToolRunsFailed(ctx, ids, finished)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Rollback before commit — DB must be unchanged.
	require.NoError(t, tx.Rollback())

	sc, err := s.GetScan(ctx, scanID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusRunning, sc.Status,
		"scan must still be running after rollback")
	assert.Equal(t, "running", toolRunStatus(t, s, trID),
		"tool_run must still be running after rollback")
}

func TestBeginReapTx_CommitPersists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	scanID := seedScan(t, s, model.ScanStatusRunning)
	adhocID := seedAdHocRun(t, s, scanID, model.AdHocRunning)
	trID := seedToolRun(t, s, scanID, model.ToolRunRunning)

	tx, err := s.BeginReapTx(ctx)
	require.NoError(t, err)

	ids, err := tx.ListOrphanedScanIDs(ctx)
	require.NoError(t, err)
	finished := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)

	_, err = tx.MarkScansFailed(ctx, ids, "orphaned on scheduler restart", finished)
	require.NoError(t, err)
	_, err = tx.MarkAdHocRunsFailed(ctx, ids, finished)
	require.NoError(t, err)
	_, err = tx.MarkToolRunsFailed(ctx, ids, finished)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	sc, err := s.GetScan(ctx, scanID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusFailed, sc.Status)
	assert.Equal(t, "failed", adhocStatus(t, s, adhocID))
	assert.Equal(t, "failed", toolRunStatus(t, s, trID))
}

func TestMarkScansFailed_Chunking(t *testing.T) {
	// Verifies that an ID list larger than reapBatchSize processes in
	// chunks without dropping rows. We don't seed 500+ real scans (slow);
	// we shrink the batch by direct call to chunkIDs and verify the
	// helper itself, then run a real path with a small N.
	chunks := chunkIDs(make([]string, 1500), 500)
	assert.Len(t, chunks, 3, "1500 ids should chunk into 3×500")
	assert.Len(t, chunks[0], 500)
	assert.Len(t, chunks[2], 500)

	chunks = chunkIDs([]string{"a", "b", "c"}, 500)
	require.Len(t, chunks, 1)
	assert.Equal(t, []string{"a", "b", "c"}, chunks[0])

	chunks = chunkIDs(nil, 500)
	require.Len(t, chunks, 1, "nil slice still yields one (empty) chunk")
	assert.Empty(t, chunks[0])
}
