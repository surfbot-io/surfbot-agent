package model

import "time"

type ToolRunStatus string

const (
	ToolRunRunning   ToolRunStatus = "running"
	ToolRunCompleted ToolRunStatus = "completed"
	ToolRunFailed    ToolRunStatus = "failed"
	ToolRunSkipped   ToolRunStatus = "skipped"
	ToolRunTimeout   ToolRunStatus = "timeout"
)

type ToolRun struct {
	ID            string         `json:"id"`
	ScanID        string         `json:"scan_id"`
	ToolName      string         `json:"tool_name"`
	Phase         string         `json:"phase"`
	Status        ToolRunStatus  `json:"status"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	DurationMs    int64          `json:"duration_ms"`
	TargetsCount  int            `json:"targets_count"`
	FindingsCount int            `json:"findings_count"`
	OutputSummary string         `json:"output_summary"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	ExitCode      int            `json:"exit_code"`
	Config        map[string]any `json:"config"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
