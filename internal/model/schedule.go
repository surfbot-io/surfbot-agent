package model

import (
	"encoding/json"
	"time"
)

// Schedule is a per-target recurrence definition. A target may own zero
// or more schedules, each with its own RRULE, tool config, and optional
// maintenance window. Introduced by SPEC-SCHED1.
type Schedule struct {
	ID                string             `json:"id"`
	TargetID          string             `json:"target_id"`
	Name              string             `json:"name"`
	RRule             string             `json:"rrule"`
	DTStart           time.Time          `json:"dtstart"`
	Timezone          string             `json:"timezone"`
	TemplateID        *string            `json:"template_id,omitempty"`
	ToolConfig        ToolConfig         `json:"tool_config"`
	Overrides         []string           `json:"overrides"`
	MaintenanceWindow *MaintenanceWindow `json:"maintenance_window,omitempty"`
	Enabled           bool               `json:"enabled"`
	NextRunAt         *time.Time         `json:"next_run_at,omitempty"`
	LastRunAt         *time.Time         `json:"last_run_at,omitempty"`
	LastRunStatus     *ScheduleRunStatus `json:"last_run_status,omitempty"`
	LastScanID        *string            `json:"last_scan_id,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// ScheduleRunStatus is the outcome of the most recent attempt to fire a
// schedule. The empty string is not a valid value — use nil on the
// Schedule.LastRunStatus pointer when no run has happened yet.
type ScheduleRunStatus string

const (
	ScheduleRunSuccess         ScheduleRunStatus = "success"
	ScheduleRunFailed          ScheduleRunStatus = "failed"
	ScheduleRunPausedBlackout  ScheduleRunStatus = "paused_blackout"
	ScheduleRunSkippedOverlap  ScheduleRunStatus = "skipped_overlap"
	ScheduleRunSkippedBlackout ScheduleRunStatus = "skipped_blackout"
)

// Template is a reusable, live-reference configuration that schedules may
// point at. Editing a template cascades to every schedule referencing it,
// except on fields listed in the schedule's `overrides`.
type Template struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	RRule             string             `json:"rrule"`
	Timezone          string             `json:"timezone"`
	ToolConfig        ToolConfig         `json:"tool_config"`
	MaintenanceWindow *MaintenanceWindow `json:"maintenance_window,omitempty"`
	IsSystem          bool               `json:"is_system"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// BlackoutScope distinguishes global vs. target-scoped blackout windows.
type BlackoutScope string

const (
	BlackoutScopeGlobal BlackoutScope = "global"
	BlackoutScopeTarget BlackoutScope = "target"
)

// BlackoutWindow defines when scans MUST NOT run. Active blackouts block
// new dispatches and cooperatively cancel in-flight scans. Scope=global
// affects every target; scope=target affects only the referenced target.
type BlackoutWindow struct {
	ID          string        `json:"id"`
	Scope       BlackoutScope `json:"scope"`
	TargetID    *string       `json:"target_id,omitempty"`
	Name        string        `json:"name"`
	RRule       string        `json:"rrule"`
	DurationSec int           `json:"duration_seconds"`
	Timezone    string        `json:"timezone"`
	Enabled     bool          `json:"enabled"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// MaintenanceWindow is a nested JSON value on Schedule, Template, and
// ScheduleDefaults. It expresses recurring periods during which scans
// should not be started. The shape matches SPEC-SCHED1 R1/R2/R4.
//
// This type is intentionally distinct from
// internal/daemon/intervalsched.MaintenanceWindow, which is the legacy
// HH:MM-based window of the singleton scheduler. The two will co-exist
// until PR SCHED1.2 flips the runtime.
type MaintenanceWindow struct {
	RRule       string `json:"rrule"`
	DurationSec int    `json:"duration_seconds"`
	Timezone    string `json:"timezone"`
}

// ScheduleDefaults is the singleton row in schedule_defaults. It supplies
// inherited values for schedules that don't override them at the template
// or schedule layer.
type ScheduleDefaults struct {
	DefaultTemplateID        *string            `json:"default_template_id,omitempty"`
	DefaultRRule             string             `json:"default_rrule"`
	DefaultTimezone          string             `json:"default_timezone"`
	DefaultToolConfig        ToolConfig         `json:"default_tool_config"`
	DefaultMaintenanceWindow *MaintenanceWindow `json:"default_maintenance_window,omitempty"`
	MaxConcurrentScans       int                `json:"max_concurrent_scans"`
	RunOnStart               bool               `json:"run_on_start"`
	JitterSeconds            int                `json:"jitter_seconds"`
	UpdatedAt                time.Time          `json:"updated_at"`
}

// AdHocRunStatus is the lifecycle status of a single ad-hoc scan run.
type AdHocRunStatus string

const (
	AdHocPending   AdHocRunStatus = "pending"
	AdHocRunning   AdHocRunStatus = "running"
	AdHocCompleted AdHocRunStatus = "completed"
	AdHocFailed    AdHocRunStatus = "failed"
	AdHocCanceled  AdHocRunStatus = "canceled"
)

// AdHocScanRun is a one-off scan invocation that is not tied to a
// schedule's RRULE expansion. Sources include `cli`, `webui:<user>`,
// `api:<token_id>`, and `schedule-runonce:<schedule_id>`.
type AdHocScanRun struct {
	ID          string         `json:"id"`
	TargetID    string         `json:"target_id"`
	TemplateID  *string        `json:"template_id,omitempty"`
	ToolConfig  ToolConfig     `json:"tool_config"`
	InitiatedBy string         `json:"initiated_by"`
	Reason      string         `json:"reason"`
	ScanID      *string        `json:"scan_id,omitempty"`
	Status      AdHocRunStatus `json:"status"`
	RequestedAt time.Time      `json:"requested_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// ToolConfig is the JSON-backed per-tool parameter map used by Schedule,
// Template, ScheduleDefaults, and AdHocScanRun. Keys are tool names
// (e.g. "nuclei", "naabu"); values are the serialized params struct for
// that tool. Unknown tool names are rejected by ValidateToolConfig at
// save time — see internal/model/tool_params.go.
type ToolConfig map[string]json.RawMessage

// GetTool decodes the params struct for a single tool out of a
// ToolConfig. Returns false if the tool key is absent or the payload
// does not decode into T. T is the tool's concrete *Params struct (e.g.
// NucleiParams).
func GetTool[T any](tc ToolConfig, toolName string) (T, bool) {
	var zero T
	raw, ok := tc[toolName]
	if !ok {
		return zero, false
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, false
	}
	return v, true
}

// SetTool encodes and stores a tool's params struct into a ToolConfig.
// Returns an error if params cannot be JSON-encoded.
func SetTool[T any](tc ToolConfig, toolName string, params T) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	tc[toolName] = raw
	return nil
}
