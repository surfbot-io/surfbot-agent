package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// DefaultEstimatedDurationSeconds is the overlap-check window applied when
// a CreateScheduleRequest omits estimated_duration_seconds. One hour is a
// conservative default for a full scan on a medium target.
const DefaultEstimatedDurationSeconds = 3600

// overlapHorizon bounds the expansion window used by ValidateNoOverlap
// when a new schedule is created. Seven days catches near-term conflicts
// without ballooning the per-request compute budget.
const overlapHorizon = 7 * 24 * time.Hour

// ScheduleResponse mirrors model.Schedule but uses stable JSON tags with
// ISO-8601 timestamps. Internal fields that are not part of the public
// API (nothing today — kept explicit for future-proofing) are excluded
// here rather than via a `-` tag on the model.
type ScheduleResponse struct {
	ID                string                   `json:"id"`
	TargetID          string                   `json:"target_id"`
	Name              string                   `json:"name"`
	RRule             string                   `json:"rrule"`
	DTStart           time.Time                `json:"dtstart"`
	Timezone          string                   `json:"timezone"`
	TemplateID        *string                  `json:"template_id,omitempty"`
	ToolConfig        model.ToolConfig         `json:"tool_config"`
	Overrides         []string                 `json:"overrides"`
	MaintenanceWindow *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
	Status            string                   `json:"status"`
	NextRunAt         *time.Time               `json:"next_run_at,omitempty"`
	LastRunAt         *time.Time               `json:"last_run_at,omitempty"`
	LastRunStatus     *model.ScheduleRunStatus `json:"last_run_status,omitempty"`
	LastScanID        *string                  `json:"last_scan_id,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// CreateScheduleRequest is the POST /api/v1/schedules body. TargetID,
// RRule, DTStart, and Timezone are required. EstimatedDurationSeconds
// is optional and drives the overlap check; it is NOT persisted (no
// column exists for it in schema 0004).
type CreateScheduleRequest struct {
	TargetID                 string                   `json:"target_id"`
	Name                     string                   `json:"name"`
	RRule                    string                   `json:"rrule"`
	DTStart                  time.Time                `json:"dtstart"`
	Timezone                 string                   `json:"timezone"`
	TemplateID               *string                  `json:"template_id,omitempty"`
	ToolConfig               model.ToolConfig         `json:"tool_config,omitempty"`
	Overrides                []string                 `json:"overrides,omitempty"`
	MaintenanceWindow        *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
	Enabled                  *bool                    `json:"enabled,omitempty"`
	EstimatedDurationSeconds int                      `json:"estimated_duration_seconds,omitempty"`
}

// UpdateScheduleRequest is the PUT /api/v1/schedules/{id} body. All
// fields are optional — only those set overwrite the stored row. When
// RRule or DTStart change, the RRULE is re-validated and (when the
// effective rrule changes) the overlap check re-runs.
type UpdateScheduleRequest struct {
	Name                     *string                  `json:"name,omitempty"`
	RRule                    *string                  `json:"rrule,omitempty"`
	DTStart                  *time.Time               `json:"dtstart,omitempty"`
	Timezone                 *string                  `json:"timezone,omitempty"`
	TemplateID               *string                  `json:"template_id,omitempty"`
	ClearTemplate            bool                     `json:"clear_template,omitempty"`
	ToolConfig               model.ToolConfig         `json:"tool_config,omitempty"`
	Overrides                []string                 `json:"overrides,omitempty"`
	MaintenanceWindow        *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
	ClearMaintenanceWindow   bool                     `json:"clear_maintenance_window,omitempty"`
	Enabled                  *bool                    `json:"enabled,omitempty"`
	EstimatedDurationSeconds int                      `json:"estimated_duration_seconds,omitempty"`
}

func (h *handlers) routeSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSchedules(w, r)
	case http.MethodPost:
		h.createSchedule(w, r)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

// routeSchedulesSubtree dispatches /api/v1/schedules/{id}(/pause|/resume).
// Subpaths handled by their own registrations (/upcoming, /bulk) take
// precedence via ServeMux's longest-prefix match so this function only
// sees the CRUD shapes.
func (h *handlers) routeSchedulesSubtree(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/schedules/")
	if rest == "" {
		writeProblem(w, http.StatusBadRequest, "/problems/missing-id",
			"Missing schedule id", "", nil)
		return
	}
	id, action, _ := strings.Cut(rest, "/")
	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			h.getSchedule(w, r, id)
		case http.MethodPut:
			h.updateSchedule(w, r, id)
		case http.MethodDelete:
			h.deleteSchedule(w, r, id)
		default:
			methodNotAllowed(w, "GET, PUT, DELETE")
		}
	case "pause":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.setScheduleEnabled(w, r, id, false)
	case "resume":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.setScheduleEnabled(w, r, id, true)
	default:
		writeProblem(w, http.StatusNotFound, "/problems/not-found",
			"Unknown subresource", "unknown action "+action, nil)
	}
}

func (h *handlers) listSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var (
		schedules []model.Schedule
		err       error
	)
	q := r.URL.Query()
	switch {
	case q.Get("template_id") != "":
		schedules, err = h.deps.ScheduleStore.ListByTemplate(ctx, q.Get("template_id"))
	case q.Get("target_id") != "":
		schedules, err = h.deps.ScheduleStore.ListByTarget(ctx, q.Get("target_id"))
	default:
		schedules, err = h.deps.ScheduleStore.ListAll(ctx)
	}
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Listing schedules failed", err.Error(), nil)
		return
	}

	// Client-side filter for status (active|paused).
	if s := q.Get("status"); s != "" {
		want := s == "active"
		if s != "active" && s != "paused" {
			writeProblem(w, http.StatusBadRequest, "/problems/invalid-query",
				"Invalid status filter", "status must be 'active' or 'paused'", nil)
			return
		}
		filtered := make([]model.Schedule, 0, len(schedules))
		for _, s := range schedules {
			if s.Enabled == want {
				filtered = append(filtered, s)
			}
		}
		schedules = filtered
	}

	total := int64(len(schedules))
	p := ParsePagination(r, MaxLimit)
	lo, hi := p.Offset, p.Offset+p.Limit
	if lo > len(schedules) {
		lo = len(schedules)
	}
	if hi > len(schedules) {
		hi = len(schedules)
	}
	page := schedules[lo:hi]

	items := make([]ScheduleResponse, 0, len(page))
	for _, s := range page {
		items = append(items, toScheduleResponse(s))
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[ScheduleResponse]{
		Items:  items,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

func (h *handlers) getSchedule(w http.ResponseWriter, r *http.Request, id string) {
	s, err := h.deps.ScheduleStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Schedule not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading schedule failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, toScheduleResponse(*s))
}

func (h *handlers) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}

	if fe := validateCreateScheduleRequest(&req); len(fe) > 0 {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Required fields missing", "", fe)
		return
	}

	if _, err := rrule.ValidateRRule(req.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// Auto-mark rrule/timezone/maintenance_window as overridden when the
	// caller supplied explicit values. The cascade resolver otherwise
	// falls through to template/defaults, which would silently discard
	// the caller's intent — a confusing UX for anyone not steeped in the
	// SCHED1 override model.
	overrides := mergeOverrides(req.Overrides, req.RRule != "", req.Timezone != "", req.MaintenanceWindow != nil)
	sched := model.Schedule{
		TargetID:          req.TargetID,
		Name:              req.Name,
		RRule:             req.RRule,
		DTStart:           req.DTStart,
		Timezone:          req.Timezone,
		TemplateID:        req.TemplateID,
		ToolConfig:        req.ToolConfig,
		Overrides:         overrides,
		MaintenanceWindow: req.MaintenanceWindow,
		Enabled:           enabled,
	}

	// FK existence checks. target must exist; template if supplied.
	if _, err := h.deps.Store.GetTarget(r.Context(), req.TargetID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
				"Target not found", "",
				[]FieldError{{Field: "target_id", Message: "no target with that id"}})
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading target failed", err.Error(), nil)
		return
	}
	var tmpl *model.Template
	if req.TemplateID != nil && *req.TemplateID != "" {
		t, err := h.deps.TemplateStore.Get(r.Context(), *req.TemplateID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
					"Template not found", "",
					[]FieldError{{Field: "template_id", Message: "no template with that id"}})
				return
			}
			writeProblem(w, http.StatusInternalServerError, "/problems/store",
				"Loading template failed", err.Error(), nil)
			return
		}
		tmpl = t
	}

	// Overlap check against existing schedules for the same target.
	if fe, err := h.overlapCheck(r.Context(), sched, tmpl, req.EstimatedDurationSeconds); err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Overlap check failed", err.Error(), nil)
		return
	} else if len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/overlap",
			"Schedule overlaps with existing schedules", "", fe)
		return
	}

	if err := h.deps.ScheduleStore.Create(r.Context(), &sched); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			writeProblem(w, http.StatusConflict, "/problems/already-exists",
				"Schedule with that name already exists for the target", "", nil)
			return
		}
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Create failed", err.Error(), nil)
		return
	}

	// Best-effort next-run computation and cascade refresh.
	h.refreshNextRun(r.Context(), &sched, tmpl)
	h.cascadeForTemplate(r.Context(), sched.TemplateID)

	writeJSON(w, http.StatusCreated, toScheduleResponse(sched))
}

func (h *handlers) updateSchedule(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}

	cur, err := h.deps.ScheduleStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Schedule not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading schedule failed", err.Error(), nil)
		return
	}

	// Apply partial updates.
	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.RRule != nil {
		cur.RRule = *req.RRule
	}
	if req.DTStart != nil {
		cur.DTStart = *req.DTStart
	}
	if req.Timezone != nil {
		cur.Timezone = *req.Timezone
	}
	if req.ClearTemplate {
		cur.TemplateID = nil
	} else if req.TemplateID != nil {
		cur.TemplateID = req.TemplateID
	}
	if req.ToolConfig != nil {
		cur.ToolConfig = req.ToolConfig
	}
	if req.Overrides != nil {
		cur.Overrides = req.Overrides
	}
	if req.ClearMaintenanceWindow {
		cur.MaintenanceWindow = nil
	} else if req.MaintenanceWindow != nil {
		cur.MaintenanceWindow = req.MaintenanceWindow
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}

	if _, err := rrule.ValidateRRule(cur.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}

	var tmpl *model.Template
	if cur.TemplateID != nil && *cur.TemplateID != "" {
		t, terr := h.deps.TemplateStore.Get(r.Context(), *cur.TemplateID)
		if terr != nil {
			if errors.Is(terr, storage.ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
					"Template not found", "",
					[]FieldError{{Field: "template_id", Message: "no template with that id"}})
				return
			}
			writeProblem(w, http.StatusInternalServerError, "/problems/store",
				"Loading template failed", terr.Error(), nil)
			return
		}
		tmpl = t
	}

	if fe, oerr := h.overlapCheck(r.Context(), *cur, tmpl, req.EstimatedDurationSeconds); oerr != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Overlap check failed", oerr.Error(), nil)
		return
	} else if len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/overlap",
			"Schedule overlaps with existing schedules", "", fe)
		return
	}

	if err := h.deps.ScheduleStore.Update(r.Context(), cur); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Update failed", err.Error(), nil)
		return
	}
	h.refreshNextRun(r.Context(), cur, tmpl)
	h.cascadeForTemplate(r.Context(), cur.TemplateID)

	writeJSON(w, http.StatusOK, toScheduleResponse(*cur))
}

func (h *handlers) deleteSchedule(w http.ResponseWriter, r *http.Request, id string) {
	// Schema 0004 has no `deleted_at` column on scan_schedules — the
	// storage layer does a hard delete. When a soft-delete column lands
	// this handler is the only one that changes.
	cur, err := h.deps.ScheduleStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Schedule not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading schedule failed", err.Error(), nil)
		return
	}
	if err := h.deps.ScheduleStore.Delete(r.Context(), id); err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Delete failed", err.Error(), nil)
		return
	}
	h.cascadeForTemplate(r.Context(), cur.TemplateID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) setScheduleEnabled(w http.ResponseWriter, r *http.Request, id string, enabled bool) {
	// Pause/resume are idempotent — calling pause twice on a paused
	// schedule returns 200 with the same body rather than an error.
	cur, err := h.deps.ScheduleStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Schedule not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading schedule failed", err.Error(), nil)
		return
	}
	if cur.Enabled != enabled {
		cur.Enabled = enabled
		if err := h.deps.ScheduleStore.Update(r.Context(), cur); err != nil {
			writeProblem(w, http.StatusInternalServerError, "/problems/store",
				"Pause/resume failed", err.Error(), nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, toScheduleResponse(*cur))
}

// overlapCheck re-runs ValidateNoOverlap against the other schedules
// already attached to the same target. When deps.Store is nil (only in
// ad-hoc tests that exercise a single handler) the check is skipped.
func (h *handlers) overlapCheck(ctx context.Context, cand model.Schedule, candTmpl *model.Template, estDuration int) ([]FieldError, error) {
	if h.deps.ScheduleStore == nil {
		return nil, nil
	}
	existing, err := h.deps.ScheduleStore.ListByTarget(ctx, cand.TargetID)
	if err != nil {
		return nil, err
	}
	tmpls := map[string]model.Template{}
	if h.deps.TemplateStore != nil {
		for _, s := range existing {
			if s.TemplateID == nil || *s.TemplateID == "" {
				continue
			}
			if _, ok := tmpls[*s.TemplateID]; ok {
				continue
			}
			t, terr := h.deps.TemplateStore.Get(ctx, *s.TemplateID)
			if terr == nil && t != nil {
				tmpls[*s.TemplateID] = *t
			}
		}
	}
	defaults := model.ScheduleDefaults{}
	if h.deps.DefaultsStore != nil {
		if d, derr := h.deps.DefaultsStore.Get(ctx); derr == nil && d != nil {
			defaults = *d
		}
	}
	dur := time.Duration(estDuration) * time.Second
	if estDuration <= 0 {
		dur = time.Duration(DefaultEstimatedDurationSeconds) * time.Second
	}
	conflicts, err := intervalsched.ValidateNoOverlap(cand, candTmpl, existing, tmpls, defaults, overlapHorizon, dur)
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 {
		return nil, nil
	}
	fe := make([]FieldError, 0, len(conflicts))
	for _, c := range conflicts {
		fe = append(fe, FieldError{
			Field: "rrule",
			Message: fmt.Sprintf("overlaps schedule %s at %s vs %s",
				c.ScheduleID, c.OccurrenceA.Format(time.RFC3339), c.OccurrenceB.Format(time.RFC3339)),
		})
	}
	return fe, nil
}

// refreshNextRun updates scan_schedules.next_run_at for `s` when an
// Expander is configured. Failure is logged via the problem response
// path but doesn't block the 2xx — the master ticker's next RecordRun
// recomputes regardless.
func (h *handlers) refreshNextRun(ctx context.Context, s *model.Schedule, tmpl *model.Template) {
	if h.deps.Expander == nil || !s.Enabled {
		return
	}
	next, err := h.deps.Expander.ComputeNextRunAt(*s, tmpl)
	if err != nil {
		return
	}
	_ = h.deps.ScheduleStore.SetNextRunAt(ctx, s.ID, next)
	s.NextRunAt = next
}

// cascadeForTemplate refreshes next_run_at for every schedule attached
// to templateID. No-op when the template pointer is nil or the expander
// isn't configured (tests without a scheduler skip the cascade).
func (h *handlers) cascadeForTemplate(ctx context.Context, templateID *string) {
	if templateID == nil || *templateID == "" || h.deps.Expander == nil {
		return
	}
	_, _ = intervalsched.RecomputeNextRunForTemplate(ctx, *templateID,
		h.deps.ScheduleStore, h.deps.TemplateStore, h.deps.Expander)
}

// mergeOverrides adds the override flag for any non-empty field supplied
// by the caller. Callers that already listed a field in explicit
// Overrides see no duplicate — the set is deduped.
func mergeOverrides(explicit []string, rrule, timezone, maintenance bool) []string {
	set := map[string]bool{}
	for _, o := range explicit {
		set[o] = true
	}
	if rrule {
		set["rrule"] = true
	}
	if timezone {
		set["timezone"] = true
	}
	if maintenance {
		set["maintenance_window"] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func validateCreateScheduleRequest(req *CreateScheduleRequest) []FieldError {
	var fe []FieldError
	if strings.TrimSpace(req.TargetID) == "" {
		fe = append(fe, FieldError{Field: "target_id", Message: "required"})
	}
	if strings.TrimSpace(req.RRule) == "" {
		fe = append(fe, FieldError{Field: "rrule", Message: "required"})
	}
	if strings.TrimSpace(req.Timezone) == "" {
		fe = append(fe, FieldError{Field: "timezone", Message: "required"})
	}
	if strings.TrimSpace(req.Name) == "" {
		fe = append(fe, FieldError{Field: "name", Message: "required"})
	}
	return fe
}

func toScheduleResponse(s model.Schedule) ScheduleResponse {
	status := "active"
	if !s.Enabled {
		status = "paused"
	}
	if s.ToolConfig == nil {
		s.ToolConfig = model.ToolConfig{}
	}
	if s.Overrides == nil {
		s.Overrides = []string{}
	}
	return ScheduleResponse{
		ID:                s.ID,
		TargetID:          s.TargetID,
		Name:              s.Name,
		RRule:             s.RRule,
		DTStart:           s.DTStart,
		Timezone:          s.Timezone,
		TemplateID:        s.TemplateID,
		ToolConfig:        s.ToolConfig,
		Overrides:         s.Overrides,
		MaintenanceWindow: s.MaintenanceWindow,
		Status:            status,
		NextRunAt:         s.NextRunAt,
		LastRunAt:         s.LastRunAt,
		LastRunStatus:     s.LastRunStatus,
		LastScanID:        s.LastScanID,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}
}

// Ensure json import is always used even if marshaling helpers are
// refactored away in a future commit.
var _ = json.Marshal
