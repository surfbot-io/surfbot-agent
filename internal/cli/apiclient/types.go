// Package apiclient is the typed HTTP client the surfbot CLI uses to
// talk to the SPEC-SCHED1.3a REST API. Types are intentionally
// duplicated (not imported) from internal/api/v1 so the CLI does not
// acquire a dependency on the server package.
package apiclient

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Schedule mirrors v1.ScheduleResponse on the server side. Kept as its
// own type so the CLI never imports the API package.
type Schedule struct {
	ID                string             `json:"id"`
	TargetID          string             `json:"target_id"`
	Name              string             `json:"name"`
	RRule             string             `json:"rrule"`
	DTStart           time.Time          `json:"dtstart"`
	Timezone          string             `json:"timezone"`
	TemplateID        *string            `json:"template_id,omitempty"`
	ToolConfig        map[string]any     `json:"tool_config"`
	Overrides         []string           `json:"overrides"`
	MaintenanceWindow *MaintenanceWindow `json:"maintenance_window,omitempty"`
	Status            string             `json:"status"`
	NextRunAt         *time.Time         `json:"next_run_at,omitempty"`
	LastRunAt         *time.Time         `json:"last_run_at,omitempty"`
	LastRunStatus     *string            `json:"last_run_status,omitempty"`
	LastScanID        *string            `json:"last_scan_id,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// MaintenanceWindow is the nested JSON value used by schedules /
// templates / defaults to describe a recurring "do not run" block.
type MaintenanceWindow struct {
	RRule       string `json:"rrule"`
	DurationSec int    `json:"duration_seconds"`
	Timezone    string `json:"timezone"`
}

// Template mirrors v1.TemplateResponse.
type Template struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	RRule             string             `json:"rrule"`
	Timezone          string             `json:"timezone"`
	ToolConfig        map[string]any     `json:"tool_config"`
	MaintenanceWindow *MaintenanceWindow `json:"maintenance_window,omitempty"`
	IsSystem          bool               `json:"is_system"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// Blackout mirrors v1.BlackoutResponse.
type Blackout struct {
	ID              string    `json:"id"`
	Scope           string    `json:"scope"`
	TargetID        *string   `json:"target_id,omitempty"`
	Name            string    `json:"name"`
	RRule           string    `json:"rrule"`
	DurationSeconds int       `json:"duration_seconds"`
	Timezone        string    `json:"timezone"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ScheduleDefaults mirrors v1.ScheduleDefaultsResponse. All fields are
// persisted; the API's PUT is a full replace so the CLI must GET,
// merge, and re-PUT to do a partial update.
type ScheduleDefaults struct {
	DefaultTemplateID        *string            `json:"default_template_id,omitempty"`
	DefaultRRule             string             `json:"default_rrule"`
	DefaultTimezone          string             `json:"default_timezone"`
	DefaultToolConfig        map[string]any     `json:"default_tool_config"`
	DefaultMaintenanceWindow *MaintenanceWindow `json:"default_maintenance_window,omitempty"`
	MaxConcurrentScans       int                `json:"max_concurrent_scans"`
	RunOnStart               bool               `json:"run_on_start"`
	JitterSeconds            int                `json:"jitter_seconds"`
	UpdatedAt                time.Time          `json:"updated_at"`
}

// PaginatedResponse is the standard list-endpoint wrapper. T is the
// item type for each endpoint.
type PaginatedResponse[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// UpcomingFiring is one element of /api/v1/schedules/upcoming.items.
type UpcomingFiring struct {
	ScheduleID string    `json:"schedule_id"`
	TargetID   string    `json:"target_id"`
	TemplateID *string   `json:"template_id,omitempty"`
	FiresAt    time.Time `json:"fires_at"`
}

// UpcomingBlackout is one blackout occurrence inside the horizon.
type UpcomingBlackout struct {
	BlackoutID string    `json:"blackout_id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
}

// UpcomingResponse is the /api/v1/schedules/upcoming body.
type UpcomingResponse struct {
	Items              []UpcomingFiring   `json:"items"`
	HorizonEnd         time.Time          `json:"horizon_end"`
	BlackoutsInHorizon []UpcomingBlackout `json:"blackouts_in_horizon"`
}

// BulkScheduleResponse is the /api/v1/schedules/bulk body.
type BulkScheduleResponse struct {
	Operation string          `json:"operation"`
	Succeeded []string        `json:"succeeded"`
	Failed    []BulkItemError `json:"failed"`
}

// BulkItemError reports a per-schedule failure in a bulk op.
type BulkItemError struct {
	ScheduleID string `json:"schedule_id"`
	Error      string `json:"error"`
}

// CreateAdHocResponse is the 202 body from /api/v1/scans/ad-hoc.
type CreateAdHocResponse struct {
	AdHocRunID string `json:"ad_hoc_run_id"`
	ScanID     string `json:"scan_id,omitempty"`
}

// ---- request shapes ----

// ListSchedulesParams captures the query parameters accepted by
// GET /api/v1/schedules. Zero fields are omitted from the URL.
type ListSchedulesParams struct {
	Status     string // "active" | "paused" | ""
	TargetID   string
	TemplateID string
	Limit      int
	Offset     int
}

// CreateScheduleRequest mirrors v1.CreateScheduleRequest.
type CreateScheduleRequest struct {
	TargetID                 string             `json:"target_id"`
	Name                     string             `json:"name"`
	RRule                    string             `json:"rrule"`
	DTStart                  time.Time          `json:"dtstart"`
	Timezone                 string             `json:"timezone"`
	TemplateID               *string            `json:"template_id,omitempty"`
	ToolConfig               map[string]any     `json:"tool_config,omitempty"`
	Overrides                []string           `json:"overrides,omitempty"`
	MaintenanceWindow        *MaintenanceWindow `json:"maintenance_window,omitempty"`
	Enabled                  *bool              `json:"enabled,omitempty"`
	EstimatedDurationSeconds int                `json:"estimated_duration_seconds,omitempty"`
}

// UpdateScheduleRequest mirrors v1.UpdateScheduleRequest.
type UpdateScheduleRequest struct {
	Name                     *string            `json:"name,omitempty"`
	RRule                    *string            `json:"rrule,omitempty"`
	DTStart                  *time.Time         `json:"dtstart,omitempty"`
	Timezone                 *string            `json:"timezone,omitempty"`
	TemplateID               *string            `json:"template_id,omitempty"`
	ClearTemplate            bool               `json:"clear_template,omitempty"`
	ToolConfig               map[string]any     `json:"tool_config,omitempty"`
	Overrides                []string           `json:"overrides,omitempty"`
	MaintenanceWindow        *MaintenanceWindow `json:"maintenance_window,omitempty"`
	ClearMaintenanceWindow   bool               `json:"clear_maintenance_window,omitempty"`
	Enabled                  *bool              `json:"enabled,omitempty"`
	EstimatedDurationSeconds int                `json:"estimated_duration_seconds,omitempty"`
}

// CreateTemplateRequest mirrors v1.CreateTemplateRequest.
type CreateTemplateRequest struct {
	Name              string             `json:"name"`
	Description       string             `json:"description,omitempty"`
	RRule             string             `json:"rrule"`
	Timezone          string             `json:"timezone,omitempty"`
	ToolConfig        map[string]any     `json:"tool_config,omitempty"`
	MaintenanceWindow *MaintenanceWindow `json:"maintenance_window,omitempty"`
}

// UpdateTemplateRequest mirrors v1.UpdateTemplateRequest.
type UpdateTemplateRequest struct {
	Name                   *string            `json:"name,omitempty"`
	Description            *string            `json:"description,omitempty"`
	RRule                  *string            `json:"rrule,omitempty"`
	Timezone               *string            `json:"timezone,omitempty"`
	ToolConfig             map[string]any     `json:"tool_config,omitempty"`
	MaintenanceWindow      *MaintenanceWindow `json:"maintenance_window,omitempty"`
	ClearMaintenanceWindow bool               `json:"clear_maintenance_window,omitempty"`
}

// CreateBlackoutRequest mirrors v1.CreateBlackoutRequest. Scope
// defaults to "global" on the server when empty.
type CreateBlackoutRequest struct {
	Scope           string  `json:"scope,omitempty"`
	TargetID        *string `json:"target_id,omitempty"`
	Name            string  `json:"name"`
	RRule           string  `json:"rrule"`
	DurationSeconds int     `json:"duration_seconds"`
	Timezone        string  `json:"timezone,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// UpdateBlackoutRequest mirrors v1.UpdateBlackoutRequest.
type UpdateBlackoutRequest struct {
	Scope           *string `json:"scope,omitempty"`
	TargetID        *string `json:"target_id,omitempty"`
	ClearTarget     bool    `json:"clear_target,omitempty"`
	Name            *string `json:"name,omitempty"`
	RRule           *string `json:"rrule,omitempty"`
	DurationSeconds *int    `json:"duration_seconds,omitempty"`
	Timezone        *string `json:"timezone,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// UpdateScheduleDefaultsRequest mirrors v1.UpdateScheduleDefaultsRequest.
type UpdateScheduleDefaultsRequest struct {
	DefaultTemplateID        *string            `json:"default_template_id,omitempty"`
	DefaultRRule             string             `json:"default_rrule"`
	DefaultTimezone          string             `json:"default_timezone"`
	DefaultToolConfig        map[string]any     `json:"default_tool_config,omitempty"`
	DefaultMaintenanceWindow *MaintenanceWindow `json:"default_maintenance_window,omitempty"`
	MaxConcurrentScans       int                `json:"max_concurrent_scans"`
	RunOnStart               bool               `json:"run_on_start"`
	JitterSeconds            int                `json:"jitter_seconds"`
}

// UpcomingParams captures query parameters for
// GET /api/v1/schedules/upcoming.
type UpcomingParams struct {
	Horizon  time.Duration
	Limit    int
	TargetID string
}

// BulkScheduleRequest mirrors v1.BulkScheduleRequest.
type BulkScheduleRequest struct {
	Operation      string                 `json:"operation"`
	ScheduleIDs    []string               `json:"schedule_ids"`
	CreateTemplate *CreateScheduleRequest `json:"create_template,omitempty"`
}

// CreateAdHocRequest mirrors v1.CreateAdHocRequest. A nil or empty
// ToolConfigOverride is omitted from the request body so the server's
// template+defaults cascade fills it in.
type CreateAdHocRequest struct {
	TargetID           string                     `json:"target_id"`
	TemplateID         *string                    `json:"template_id,omitempty"`
	ToolConfigOverride map[string]json.RawMessage `json:"tool_config_override,omitempty"`
	RequestedBy        string                     `json:"requested_by,omitempty"`
	Reason             string                     `json:"reason,omitempty"`
}

// ---- error type ----

// FieldError is the per-field validation error shape returned inside
// an APIError's field_errors array. Field uses dotted JSON paths
// (e.g. `tool_config.nuclei.severity`).
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// APIError is every non-2xx response from the API. It carries the
// RFC 7807 problem+json shape plus the HTTP status. Error() formats a
// human-readable summary; callers that want structured access read the
// fields directly.
type APIError struct {
	StatusCode  int          `json:"-"`
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Status      int          `json:"status"`
	Detail      string       `json:"detail,omitempty"`
	FieldErrors []FieldError `json:"field_errors,omitempty"`
}

// Error implements error. Format: `<status> <title>: <detail>` with
// each FieldError appended on its own line.
func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", e.StatusCode, e.Title)
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	for _, fe := range e.FieldErrors {
		fmt.Fprintf(&b, "\n  - %s: %s", fe.Field, fe.Message)
	}
	return b.String()
}
