package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// ScheduleDefaultsResponse is the public shape of the singleton
// schedule_defaults row.
type ScheduleDefaultsResponse struct {
	DefaultTemplateID        *string                  `json:"default_template_id,omitempty"`
	DefaultRRule             string                   `json:"default_rrule"`
	DefaultTimezone          string                   `json:"default_timezone"`
	DefaultToolConfig        model.ToolConfig         `json:"default_tool_config"`
	DefaultMaintenanceWindow *model.MaintenanceWindow `json:"default_maintenance_window,omitempty"`
	MaxConcurrentScans       int                      `json:"max_concurrent_scans"`
	RunOnStart               bool                     `json:"run_on_start"`
	JitterSeconds            int                      `json:"jitter_seconds"`
	UpdatedAt                time.Time                `json:"updated_at"`
}

// DefaultDefaults is the shape returned by GET when the singleton row is
// missing (e.g. on a fresh install where migration 0004 hasn't run).
// The live row wins when present.
var DefaultDefaults = ScheduleDefaultsResponse{
	DefaultRRule:       "FREQ=DAILY;BYHOUR=2",
	DefaultTimezone:    "UTC",
	DefaultToolConfig:  model.ToolConfig{},
	MaxConcurrentScans: 4,
	RunOnStart:         false,
	JitterSeconds:      60,
}

// UpdateScheduleDefaultsRequest mirrors the response shape. PUT is a
// full replace — there is no PATCH semantics on a singleton row.
type UpdateScheduleDefaultsRequest struct {
	DefaultTemplateID        *string                  `json:"default_template_id,omitempty"`
	DefaultRRule             string                   `json:"default_rrule"`
	DefaultTimezone          string                   `json:"default_timezone"`
	DefaultToolConfig        model.ToolConfig         `json:"default_tool_config,omitempty"`
	DefaultMaintenanceWindow *model.MaintenanceWindow `json:"default_maintenance_window,omitempty"`
	MaxConcurrentScans       int                      `json:"max_concurrent_scans"`
	RunOnStart               bool                     `json:"run_on_start"`
	JitterSeconds            int                      `json:"jitter_seconds"`
}

func (h *handlers) routeScheduleDefaults(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getScheduleDefaults(w, r)
	case http.MethodPut:
		h.putScheduleDefaults(w, r)
	default:
		methodNotAllowed(w, "GET, PUT")
	}
}

func (h *handlers) getScheduleDefaults(w http.ResponseWriter, r *http.Request) {
	d, err := h.deps.DefaultsStore.Get(r.Context())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusOK, DefaultDefaults)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading defaults failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, toDefaultsResponse(*d))
}

func (h *handlers) putScheduleDefaults(w http.ResponseWriter, r *http.Request) {
	var req UpdateScheduleDefaultsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	if fe := validateDefaultsRequest(&req); len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid defaults", "", fe)
		return
	}
	if fe := validateToolConfigFields(req.DefaultToolConfig, "default_tool_config"); len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid default_tool_config", "", fe)
		return
	}

	d := model.ScheduleDefaults{
		DefaultTemplateID:        req.DefaultTemplateID,
		DefaultRRule:             req.DefaultRRule,
		DefaultTimezone:          req.DefaultTimezone,
		DefaultToolConfig:        req.DefaultToolConfig,
		DefaultMaintenanceWindow: req.DefaultMaintenanceWindow,
		MaxConcurrentScans:       req.MaxConcurrentScans,
		RunOnStart:               req.RunOnStart,
		JitterSeconds:            req.JitterSeconds,
	}
	if err := h.deps.DefaultsStore.Update(r.Context(), &d); err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Update failed", err.Error(), nil)
		return
	}

	// Cascade: every schedule not overriding rrule/timezone/MW inherits
	// from defaults (directly, when no template, or transitively via a
	// template's empty field). Recompute next_run_at across all
	// templates so the tick loop picks up the new values.
	if h.deps.Expander != nil && h.deps.TemplateStore != nil {
		if tmpls, lerr := h.deps.TemplateStore.List(r.Context()); lerr == nil {
			for _, t := range tmpls {
				_, _ = intervalsched.RecomputeNextRunForTemplate(r.Context(), t.ID,
					h.deps.ScheduleStore, h.deps.TemplateStore, h.deps.Expander)
			}
		}
	}

	writeJSON(w, http.StatusOK, toDefaultsResponse(d))
}

func validateDefaultsRequest(req *UpdateScheduleDefaultsRequest) []FieldError {
	var fe []FieldError
	if req.MaxConcurrentScans < 1 {
		fe = append(fe, FieldError{Field: "max_concurrent_scans", Message: "must be >= 1"})
	}
	if req.JitterSeconds < 0 {
		fe = append(fe, FieldError{Field: "jitter_seconds", Message: "must be >= 0"})
	}
	if req.DefaultTimezone != "" {
		if _, err := time.LoadLocation(req.DefaultTimezone); err != nil {
			fe = append(fe, FieldError{Field: "default_timezone", Message: err.Error()})
		}
	}
	return fe
}

func toDefaultsResponse(d model.ScheduleDefaults) ScheduleDefaultsResponse {
	if d.DefaultToolConfig == nil {
		d.DefaultToolConfig = model.ToolConfig{}
	}
	return ScheduleDefaultsResponse{
		DefaultTemplateID:        d.DefaultTemplateID,
		DefaultRRule:             d.DefaultRRule,
		DefaultTimezone:          d.DefaultTimezone,
		DefaultToolConfig:        d.DefaultToolConfig,
		DefaultMaintenanceWindow: d.DefaultMaintenanceWindow,
		MaxConcurrentScans:       d.MaxConcurrentScans,
		RunOnStart:               d.RunOnStart,
		JitterSeconds:            d.JitterSeconds,
		UpdatedAt:                d.UpdatedAt,
	}
}
