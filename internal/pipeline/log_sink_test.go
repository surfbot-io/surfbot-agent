package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// fakeWriter is a thread-safe in-memory scanLogWriter for sink tests.
// It records every batch InsertScanLogs receives so tests can assert
// ordering, ANSI stripping, and batch boundaries.
type fakeWriter struct {
	mu      sync.Mutex
	logs    []model.ScanLog
	batches int
	failNxt error
}

func (w *fakeWriter) InsertScanLogs(ctx context.Context, logs []model.ScanLog) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failNxt != nil {
		err := w.failNxt
		w.failNxt = nil
		return err
	}
	w.batches++
	w.logs = append(w.logs, logs...)
	return nil
}

func (w *fakeWriter) snapshot() []model.ScanLog {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]model.ScanLog, len(w.logs))
	copy(out, w.logs)
	return out
}

func TestSQLiteLogSink_BatchedInsert(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{BatchSize: 10, FlushInterval: 50 * time.Millisecond})
	defer sink.Close()

	for i := 0; i < 25; i++ {
		sink.Emit(context.Background(), "scan-1", model.LogLevelInfo, "scanner", "line")
	}
	require.NoError(t, sink.Close())
	got := w.snapshot()
	assert.Equal(t, 25, len(got), "all 25 lines should reach the writer")
}

func TestSQLiteLogSink_AnsiStripped(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{BatchSize: 1, FlushInterval: 10 * time.Millisecond})

	sink.Emit(context.Background(), "scan-1", model.LogLevelInfo, "scanner", "\x1b[31mhello\x1b[0m world")
	require.NoError(t, sink.Close())
	got := w.snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, "hello world", got[0].Text, "ANSI sequences must be stripped before persistence")
}

func TestSQLiteLogSink_CloseFlushesOutstanding(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{BatchSize: 100, FlushInterval: 5 * time.Second})
	for i := 0; i < 5; i++ {
		sink.Emit(context.Background(), "scan-1", model.LogLevelInfo, "scanner", "tail")
	}
	require.NoError(t, sink.Close())
	assert.Equal(t, 5, len(w.snapshot()), "Close must flush outstanding lines synchronously")
}

func TestSQLiteLogSink_CloseIdempotent(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{})
	require.NoError(t, sink.Close())
	require.NoError(t, sink.Close(), "Close must be idempotent")
}

func TestSQLiteLogSink_BoundedChannelDropsOldest(t *testing.T) {
	w := &fakeWriter{}
	// Cap=4, batch=4, flush slow → channel saturates fast.
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{
		ChannelCapacity: 4,
		BatchSize:       4,
		FlushInterval:   500 * time.Millisecond,
	})
	for i := 0; i < 200; i++ {
		sink.Emit(context.Background(), "scan-1", model.LogLevelInfo, "scanner", "load")
	}
	require.NoError(t, sink.Close())
	dropped := sink.Stats()
	assert.Greater(t, dropped, int64(0), "overflow must be accounted as drops")
}

func TestSQLiteLogSink_LifecycleEvents(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{BatchSize: 1, FlushInterval: 10 * time.Millisecond})
	ctx := context.Background()
	scanID := "scan-abc"
	sink.ScanStarted(ctx, scanID, "example.com", model.ScanTypeFull)
	sink.PhaseStarted(ctx, scanID, "discovery", "subfinder")
	sink.ToolStarted(ctx, scanID, "tr-1", "subfinder", "discovery", 1)
	sink.ToolStderr(ctx, scanID, "tr-1", "subfinder", "rate limited")
	sink.ToolCompleted(ctx, scanID, "tr-1", "subfinder", 42, 100, "Discovered 100 subdomains")
	sink.ScanCompleted(ctx, scanID, 1234)
	require.NoError(t, sink.Close())
	got := w.snapshot()
	require.Len(t, got, 6)
	assert.Equal(t, "scanner", got[0].Source)
	assert.Equal(t, model.LogLevelInfo, got[0].Level)
	assert.Contains(t, got[0].Text, "scan started")
	assert.Equal(t, "subfinder", got[2].Source)
	assert.Equal(t, "tr-1", got[2].ToolRunID)
	assert.Equal(t, model.LogLevelWarn, got[3].Level, "ToolStderr defaults to warn")
	assert.Contains(t, got[5].Text, "scan completed")
}

func TestSQLiteLogSink_DefaultsLevelToInfo(t *testing.T) {
	w := &fakeWriter{}
	sink := NewSQLiteLogSink(w, SQLiteLogSinkOptions{BatchSize: 1, FlushInterval: 5 * time.Millisecond})
	sink.Emit(context.Background(), "scan-1", "", "scanner", "blank-level")
	require.NoError(t, sink.Close())
	got := w.snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, model.LogLevelInfo, got[0].Level)
}

func TestNoopSink_ImplementsLogSink(t *testing.T) {
	var _ LogSink = NoopSink{}
	NoopSink{}.ScanStarted(context.Background(), "x", "y", model.ScanTypeFull) // no panic
	require.NoError(t, NoopSink{}.Close())
}
