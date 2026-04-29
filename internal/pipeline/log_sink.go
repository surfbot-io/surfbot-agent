package pipeline

import (
	"context"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// LogSink receives structured pipeline events. Implementations:
//   - NoopSink: drops everything (default; tests + CLI-only paths
//     where SQLite isn't desired).
//   - SQLiteLogSink: persists batched to scan_logs (issue #52).
//
// All methods MUST be non-blocking under nominal load. Implementations
// that persist asynchronously (e.g. SQLite via a buffered channel)
// guarantee enqueue is < 1µs; backpressure is drop-oldest, never
// blocking the caller.
//
// LogSink is *additive* over the existing CLI printer (`pp.muted`
// etc.) — these methods don't replace terminal output. The pipeline
// calls both. Missing a sink call costs the UI one log line; missing
// a `pp.muted` call breaks the operator's terminal experience. We
// bias toward the safe direction.
type LogSink interface {
	ScanStarted(ctx context.Context, scanID, target string, scanType model.ScanType)
	ScanCompleted(ctx context.Context, scanID string, durationMs int64)
	ScanFailed(ctx context.Context, scanID, errMsg string)
	ScanCancelled(ctx context.Context, scanID, reason string)

	PhaseStarted(ctx context.Context, scanID, phase, toolName string)

	ToolStarted(ctx context.Context, scanID, toolRunID, toolName, phase string, inputCount int)
	ToolCompleted(ctx context.Context, scanID, toolRunID, toolName string, durationMs int64, outputCount int, summary string)
	ToolFailed(ctx context.Context, scanID, toolRunID, toolName, errMsg string)
	ToolSkipped(ctx context.Context, scanID, toolName, reason string)

	// ToolStderr emits a single line of accumulated tool stderr at
	// level=warn. Multi-line stderr should be split by the caller
	// before invoking this.
	ToolStderr(ctx context.Context, scanID, toolRunID, toolName, line string)

	// Emit is the catch-all for events that don't fit a specialized
	// method. Source should match a tool name when applicable,
	// "scanner" otherwise.
	Emit(ctx context.Context, scanID string, level model.LogLevel, source, text string)

	// Close flushes any in-flight writes and releases resources. Safe
	// to call multiple times; only the first call has effect.
	Close() error
}

// NoopSink discards every event. Used by tests and CLI-only entry
// points without a SQLite handle wired up. Implements the full
// LogSink contract so callers never need a nil check.
type NoopSink struct{}

func (NoopSink) ScanStarted(context.Context, string, string, model.ScanType) {}
func (NoopSink) ScanCompleted(context.Context, string, int64)                {}
func (NoopSink) ScanFailed(context.Context, string, string)                  {}
func (NoopSink) ScanCancelled(context.Context, string, string)               {}
func (NoopSink) PhaseStarted(context.Context, string, string, string)        {}
func (NoopSink) ToolStarted(context.Context, string, string, string, string, int) {
}
func (NoopSink) ToolCompleted(context.Context, string, string, string, int64, int, string) {
}
func (NoopSink) ToolFailed(context.Context, string, string, string, string)   {}
func (NoopSink) ToolSkipped(context.Context, string, string, string)          {}
func (NoopSink) ToolStderr(context.Context, string, string, string, string)   {}
func (NoopSink) Emit(context.Context, string, model.LogLevel, string, string) {}
func (NoopSink) Close() error                                                 { return nil }

// Compile-time assertion: NoopSink implements LogSink.
var _ LogSink = NoopSink{}
