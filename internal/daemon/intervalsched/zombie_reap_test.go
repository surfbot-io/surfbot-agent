package intervalsched

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReapTx records every call and lets tests inject errors at any
// step. State after Commit is observable via the parent fakeBackend.
type fakeReapTx struct {
	parent *fakeBackend

	listIDs []string
	listErr error

	markScansErr error
	markAdHocErr error
	markToolsErr error

	commitErr   error
	rolledBack  bool
	committed   bool
	finalReport struct {
		errMsg     string
		finishedAt time.Time
		scansN     int
		adhocN     int
		toolsN     int
	}
}

func (f *fakeReapTx) ListOrphanedScanIDs(_ context.Context) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listIDs, nil
}

func (f *fakeReapTx) MarkScansFailed(_ context.Context, ids []string, errMsg string, finishedAt time.Time) (int, error) {
	if f.markScansErr != nil {
		return 0, f.markScansErr
	}
	f.finalReport.errMsg = errMsg
	f.finalReport.finishedAt = finishedAt
	f.finalReport.scansN = len(ids)
	return len(ids), nil
}

func (f *fakeReapTx) MarkAdHocRunsFailed(_ context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	if f.markAdHocErr != nil {
		return 0, f.markAdHocErr
	}
	// Simulate "0 or 1 adhoc per scan", driven by parent fixture.
	n := f.parent.adhocPerScan
	if n > len(scanIDs) {
		n = len(scanIDs)
	}
	f.finalReport.adhocN = n
	return n, nil
}

func (f *fakeReapTx) MarkToolRunsFailed(_ context.Context, scanIDs []string, finishedAt time.Time) (int, error) {
	if f.markToolsErr != nil {
		return 0, f.markToolsErr
	}
	n := f.parent.toolsPerScan * len(scanIDs)
	f.finalReport.toolsN = n
	return n, nil
}

func (f *fakeReapTx) Commit() error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.committed = true
	f.parent.lastCommitted = f
	return nil
}

func (f *fakeReapTx) Rollback() error {
	f.rolledBack = true
	return nil
}

type fakeBackend struct {
	tx           *fakeReapTx
	beginErr     error
	beginCount   int
	adhocPerScan int
	toolsPerScan int

	lastCommitted *fakeReapTx
}

func (f *fakeBackend) BeginReapTx(_ context.Context) (ZombieReapTx, error) {
	f.beginCount++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

// reapClock returns a fixed time and advances per-call by step so we
// can verify Duration is computed from clock.Now() (not real wall).
type reapClock struct {
	now  time.Time
	step time.Duration
}

func (c *reapClock) Now() time.Time {
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

func (c *reapClock) NewTimer(_ time.Duration) Timer { panic("unused") }

func TestReapOrphanedScans_HappyPath(t *testing.T) {
	tx := &fakeReapTx{listIDs: []string{"s1", "s2", "s3"}}
	be := &fakeBackend{tx: tx, adhocPerScan: 2, toolsPerScan: 4}
	tx.parent = be

	clk := &reapClock{now: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC), step: 5 * time.Millisecond}

	report, err := ReapOrphanedScans(context.Background(), be, clk, slog.Default())
	require.NoError(t, err)

	assert.Equal(t, 3, report.ScansReaped)
	assert.Equal(t, 2, report.AdHocRunsReaped)
	assert.Equal(t, 12, report.ToolRunsReaped, "3 scans × 4 tool_runs each")
	assert.True(t, tx.committed, "tx must be committed")
	assert.False(t, tx.rolledBack)

	assert.Equal(t, "orphaned on scheduler restart", tx.finalReport.errMsg,
		"canonical error string must be propagated verbatim")

	// finishedAt must come from the clock (start time + 1 step), not wall.
	expected := time.Date(2026, 4, 26, 10, 0, 0, int(5*time.Millisecond), time.UTC)
	assert.True(t, tx.finalReport.finishedAt.Equal(expected),
		"finishedAt should match injected clock value, got %v", tx.finalReport.finishedAt)

	assert.True(t, report.Duration > 0, "Duration should be derived from clock")
}

func TestReapOrphanedScans_EmptyOrphans(t *testing.T) {
	tx := &fakeReapTx{listIDs: nil}
	be := &fakeBackend{tx: tx}
	tx.parent = be

	clk := &reapClock{now: time.Now(), step: time.Millisecond}

	report, err := ReapOrphanedScans(context.Background(), be, clk, slog.Default())
	require.NoError(t, err)

	assert.Zero(t, report.ScansReaped)
	assert.Zero(t, report.AdHocRunsReaped)
	assert.Zero(t, report.ToolRunsReaped)
	assert.True(t, tx.committed, "empty case must still commit the (no-op) tx")
	assert.False(t, tx.rolledBack)
}

func TestReapOrphanedScans_ListErrorRollsBack(t *testing.T) {
	wantErr := errors.New("boom")
	tx := &fakeReapTx{listErr: wantErr}
	be := &fakeBackend{tx: tx}
	tx.parent = be

	_, err := ReapOrphanedScans(context.Background(), be, &reapClock{now: time.Now()}, slog.Default())
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.True(t, tx.rolledBack, "list failure must trigger rollback")
	assert.False(t, tx.committed)
}

func TestReapOrphanedScans_MidTxErrorRollsBack(t *testing.T) {
	wantErr := errors.New("adhoc update fail")
	tx := &fakeReapTx{
		listIDs:      []string{"s1"},
		markAdHocErr: wantErr,
	}
	be := &fakeBackend{tx: tx, adhocPerScan: 1, toolsPerScan: 1}
	tx.parent = be

	_, err := ReapOrphanedScans(context.Background(), be, &reapClock{now: time.Now()}, slog.Default())
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.True(t, tx.rolledBack)
	assert.False(t, tx.committed)
}

func TestReapOrphanedScans_BeginTxErrorIsSurfaced(t *testing.T) {
	be := &fakeBackend{beginErr: errors.New("can't open tx")}

	_, err := ReapOrphanedScans(context.Background(), be, &reapClock{now: time.Now()}, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin reap tx")
}

func TestReapOrphanedScans_NilLoggerUsesDefault(t *testing.T) {
	tx := &fakeReapTx{listIDs: nil}
	be := &fakeBackend{tx: tx}
	tx.parent = be

	_, err := ReapOrphanedScans(context.Background(), be, &reapClock{now: time.Now()}, nil)
	require.NoError(t, err, "nil logger must not panic")
}

func TestReapOrphanedScans_Idempotent(t *testing.T) {
	// Two consecutive calls. The fake backend keeps the same tx but
	// resets listIDs to empty after the first call to simulate the
	// idempotent contract: real DB returns no orphans on the second
	// call because the first reap already moved them out of 'running'.
	tx1 := &fakeReapTx{listIDs: []string{"s1", "s2"}}
	be := &fakeBackend{tx: tx1, adhocPerScan: 1, toolsPerScan: 2}
	tx1.parent = be

	clk := &reapClock{now: time.Now(), step: time.Millisecond}

	report1, err := ReapOrphanedScans(context.Background(), be, clk, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, 2, report1.ScansReaped)

	// Second call: fresh tx returns no orphans.
	tx2 := &fakeReapTx{listIDs: nil}
	be.tx = tx2
	tx2.parent = be

	report2, err := ReapOrphanedScans(context.Background(), be, clk, slog.Default())
	require.NoError(t, err)
	assert.Zero(t, report2.ScansReaped)
	assert.Zero(t, report2.AdHocRunsReaped)
	assert.Zero(t, report2.ToolRunsReaped)
}
