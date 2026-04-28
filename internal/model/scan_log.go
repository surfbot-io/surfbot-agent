package model

import "time"

// LogLevel is the severity of a scan log line. The four-value vocabulary
// matches the SQLite CHECK constraint in migration 0006_scan_logs.sql —
// add a value here and the migration must change in lockstep.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// ScanLog is a single structured event emitted by the pipeline during a
// scan. The webui consumes these via /api/v1/scans/:id/logs to show a
// live log stream that mirrors what the operator sees on the terminal.
//
// Source identifies the emitter:
//   - "scanner" for orchestrator-level events (scan/phase started, etc.)
//   - tool name (e.g. "subfinder", "nuclei") for tool-level events.
//
// ToolRunID is empty for orchestrator-level events. JSON omits the
// field when empty so consumers can branch on its presence.
type ScanLog struct {
	ID        int64     `json:"id"`
	ScanID    string    `json:"scan_id"`
	ToolRunID string    `json:"tool_run_id,omitempty"`
	Timestamp time.Time `json:"ts"`
	Source    string    `json:"source"`
	Level     LogLevel  `json:"level"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
