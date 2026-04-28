package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// seedScanForLogs creates a target + scan + tool_run so scan_logs FK
// references resolve. Returns the scan and tool_run IDs.
func seedScanForLogs(t *testing.T, s *SQLiteStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	target := &model.Target{Value: "logs-test.example.com", Type: model.TargetTypeDomain, Scope: "external"}
	require.NoError(t, s.CreateTarget(ctx, target))
	now := time.Now().UTC()
	scan := &model.Scan{
		TargetID:  target.ID,
		Type:      model.ScanTypeFull,
		Status:    model.ScanStatusRunning,
		Phase:     "discovery",
		StartedAt: &now,
	}
	require.NoError(t, s.CreateScan(ctx, scan))
	finished := now.Add(time.Second)
	tr := &model.ToolRun{
		ScanID:     scan.ID,
		ToolName:   "subfinder",
		Phase:      "discovery",
		Status:     model.ToolRunCompleted,
		StartedAt:  now,
		FinishedAt: &finished,
		DurationMs: 1000,
	}
	require.NoError(t, s.CreateToolRun(ctx, tr))
	return scan.ID, tr.ID
}

func TestInsertScanLogs_BatchAndRoundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, trID := seedScanForLogs(t, s)

	now := time.Now().UTC()
	logs := []model.ScanLog{
		{ScanID: scanID, ToolRunID: "", Source: "scanner", Level: model.LogLevelInfo, Text: "scan started", Timestamp: now, CreatedAt: now},
		{ScanID: scanID, ToolRunID: trID, Source: "subfinder", Level: model.LogLevelInfo, Text: "tool started", Timestamp: now.Add(time.Millisecond), CreatedAt: now.Add(time.Millisecond)},
		{ScanID: scanID, ToolRunID: trID, Source: "subfinder", Level: model.LogLevelWarn, Text: "rate limited", Timestamp: now.Add(2 * time.Millisecond), CreatedAt: now.Add(2 * time.Millisecond)},
	}
	require.NoError(t, s.InsertScanLogs(ctx, logs))

	got, err := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "scan started", got[0].Text)
	assert.Equal(t, model.LogLevelInfo, got[0].Level)
	assert.Equal(t, "", got[0].ToolRunID, "scan-level event has empty tool_run_id")
	assert.Equal(t, trID, got[1].ToolRunID)
	assert.Equal(t, model.LogLevelWarn, got[2].Level)
}

// TestInsertScanLogs_NoFKOnToolRunID is the regression guard for the
// bug that triggered the revert + re-implementation. The pipeline
// emits ToolStarted log lines BEFORE the matching tool_runs row is
// inserted, so the column must NOT be foreign-key-constrained.
// Inserting a row with a tool_run_id that doesn't exist must succeed.
func TestInsertScanLogs_NoFKOnToolRunID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, ToolRunID: "00000000-not-yet-persisted-tool-run", Source: "subfinder", Level: model.LogLevelInfo, Text: "tool started", Timestamp: time.Now(), CreatedAt: time.Now()},
	}))
	got, err := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "00000000-not-yet-persisted-tool-run", got[0].ToolRunID)
}

func TestInsertScanLogs_EmptyIsNoop(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.InsertScanLogs(context.Background(), nil))
	require.NoError(t, s.InsertScanLogs(context.Background(), []model.ScanLog{}))
}

func TestListScanLogs_PaginationCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)

	now := time.Now().UTC()
	batch := make([]model.ScanLog, 50)
	for i := range batch {
		batch[i] = model.ScanLog{
			ScanID:    scanID,
			Source:    "scanner",
			Level:     model.LogLevelInfo,
			Text:      fmt.Sprintf("line %d", i),
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}
	}
	require.NoError(t, s.InsertScanLogs(ctx, batch))

	page1, err := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID, Limit: 20})
	require.NoError(t, err)
	require.Len(t, page1, 20)
	assert.Equal(t, "line 0", page1[0].Text)
	assert.Equal(t, "line 19", page1[19].Text)

	page2, err := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID, Since: page1[19].ID, Limit: 20})
	require.NoError(t, err)
	require.Len(t, page2, 20)
	assert.Equal(t, "line 20", page2[0].Text)
	assert.Greater(t, page2[0].ID, page1[19].ID)
}

func TestListScanLogs_LevelFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)

	now := time.Now().UTC()
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, Source: "scanner", Level: model.LogLevelInfo, Text: "i", Timestamp: now, CreatedAt: now},
		{ScanID: scanID, Source: "scanner", Level: model.LogLevelWarn, Text: "w", Timestamp: now, CreatedAt: now},
		{ScanID: scanID, Source: "scanner", Level: model.LogLevelError, Text: "e", Timestamp: now, CreatedAt: now},
	}))

	got, err := s.ListScanLogs(ctx, ScanLogListOptions{
		ScanID: scanID,
		Level:  []model.LogLevel{model.LogLevelWarn, model.LogLevelError},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "w", got[0].Text)
	assert.Equal(t, "e", got[1].Text)
}

func TestListScanLogs_SourceFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, trID := seedScanForLogs(t, s)
	now := time.Now().UTC()
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, Source: "scanner", Level: model.LogLevelInfo, Text: "s", Timestamp: now, CreatedAt: now},
		{ScanID: scanID, ToolRunID: trID, Source: "subfinder", Level: model.LogLevelInfo, Text: "sf", Timestamp: now, CreatedAt: now},
		{ScanID: scanID, ToolRunID: trID, Source: "naabu", Level: model.LogLevelInfo, Text: "n", Timestamp: now, CreatedAt: now},
	}))

	got, err := s.ListScanLogs(ctx, ScanLogListOptions{
		ScanID: scanID,
		Source: []string{"subfinder", "naabu"},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestCountScanLogs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, Source: "scanner", Text: "a", Timestamp: time.Now(), CreatedAt: time.Now()},
		{ScanID: scanID, Source: "scanner", Text: "b", Timestamp: time.Now(), CreatedAt: time.Now()},
	}))
	n, err := s.CountScanLogs(ctx, scanID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestPruneScanLogsOlderThan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)
	old := time.Now().UTC().Add(-48 * time.Hour)
	fresh := time.Now().UTC()
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, Source: "scanner", Text: "old", Timestamp: old, CreatedAt: old},
		{ScanID: scanID, Source: "scanner", Text: "old2", Timestamp: old, CreatedAt: old},
		{ScanID: scanID, Source: "scanner", Text: "fresh", Timestamp: fresh, CreatedAt: fresh},
	}))
	deleted, err := s.PruneScanLogsOlderThan(ctx, time.Now().UTC().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	got, _ := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID})
	require.Len(t, got, 1)
	assert.Equal(t, "fresh", got[0].Text)
}

func TestCascade_ScanDeletePrunesLogs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, _ := seedScanForLogs(t, s)
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, Source: "scanner", Text: "a", Timestamp: time.Now(), CreatedAt: time.Now()},
	}))
	n, _ := s.CountScanLogs(ctx, scanID)
	require.Equal(t, 1, n)
	_, err := s.db.ExecContext(ctx, "DELETE FROM scans WHERE id = ?", scanID)
	require.NoError(t, err)
	n2, _ := s.CountScanLogs(ctx, scanID)
	assert.Equal(t, 0, n2, "FK CASCADE: deleting scan must drop scan_logs rows")
}

// scan_logs.tool_run_id is a plain TEXT column without an FK
// reference (see 0006_scan_logs.sql comment). Deleting the
// referenced tool_runs row must NOT touch scan_logs — the log stays
// as-is with its now-dangling but harmless pointer. This guards
// against a future "let's add the FK back" reflex.
func TestToolRunDeletePreservesLogs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scanID, trID := seedScanForLogs(t, s)
	require.NoError(t, s.InsertScanLogs(ctx, []model.ScanLog{
		{ScanID: scanID, ToolRunID: trID, Source: "subfinder", Text: "tool log", Timestamp: time.Now(), CreatedAt: time.Now()},
	}))
	_, err := s.db.ExecContext(ctx, "DELETE FROM tool_runs WHERE id = ?", trID)
	require.NoError(t, err)
	got, err := s.ListScanLogs(ctx, ScanLogListOptions{ScanID: scanID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, trID, got[0].ToolRunID,
		"deleting tool_run does not touch scan_logs (no FK on tool_run_id)")
}

func TestListScanLogs_RequiresScanID(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ListScanLogs(context.Background(), ScanLogListOptions{})
	require.Error(t, err)
}
