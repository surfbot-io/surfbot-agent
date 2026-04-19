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

// maxDependentIDsInFieldError bounds the number of dependent schedule IDs
// echoed in the 409 response body when a template DELETE without ?force
// refuses to proceed. Anything beyond this is summarized as "... and N
// more" so the payload stays small.
const maxDependentIDsInFieldError = 10

// TemplateResponse mirrors model.Template with ISO-8601 timestamps and
// a stable JSON shape.
type TemplateResponse struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Description       string                   `json:"description"`
	RRule             string                   `json:"rrule"`
	Timezone          string                   `json:"timezone"`
	ToolConfig        model.ToolConfig         `json:"tool_config"`
	MaintenanceWindow *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
	IsSystem          bool                     `json:"is_system"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// CreateTemplateRequest is the POST /api/v1/templates body.
type CreateTemplateRequest struct {
	Name              string                   `json:"name"`
	Description       string                   `json:"description,omitempty"`
	RRule             string                   `json:"rrule"`
	Timezone          string                   `json:"timezone,omitempty"`
	ToolConfig        model.ToolConfig         `json:"tool_config,omitempty"`
	MaintenanceWindow *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
}

// UpdateTemplateRequest is the PUT body. Partial — only non-nil pointer
// fields overwrite; ClearMaintenanceWindow removes the window entirely.
type UpdateTemplateRequest struct {
	Name                   *string                  `json:"name,omitempty"`
	Description            *string                  `json:"description,omitempty"`
	RRule                  *string                  `json:"rrule,omitempty"`
	Timezone               *string                  `json:"timezone,omitempty"`
	ToolConfig             model.ToolConfig         `json:"tool_config,omitempty"`
	MaintenanceWindow      *model.MaintenanceWindow `json:"maintenance_window,omitempty"`
	ClearMaintenanceWindow bool                     `json:"clear_maintenance_window,omitempty"`
}

func (h *handlers) routeTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listTemplates(w, r)
	case http.MethodPost:
		h.createTemplate(w, r)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (h *handlers) routeTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/templates/")
	if id == "" || strings.Contains(id, "/") {
		writeProblem(w, http.StatusNotFound, "/problems/not-found",
			"Unknown template subresource", "", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getTemplate(w, r, id)
	case http.MethodPut:
		h.updateTemplate(w, r, id)
	case http.MethodDelete:
		h.deleteTemplate(w, r, id)
	default:
		methodNotAllowed(w, "GET, PUT, DELETE")
	}
}

func (h *handlers) listTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.TemplateStore.List(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Listing templates failed", err.Error(), nil)
		return
	}
	total := int64(len(items))
	p := ParsePagination(r, MaxLimit)
	lo, hi := p.Offset, p.Offset+p.Limit
	if lo > len(items) {
		lo = len(items)
	}
	if hi > len(items) {
		hi = len(items)
	}
	page := items[lo:hi]
	out := make([]TemplateResponse, 0, len(page))
	for _, t := range page {
		out = append(out, toTemplateResponse(t))
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[TemplateResponse]{
		Items:  out,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

func (h *handlers) getTemplate(w http.ResponseWriter, r *http.Request, id string) {
	t, err := h.deps.TemplateStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Template not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading template failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, toTemplateResponse(*t))
}

func (h *handlers) createTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Required fields missing", "",
			[]FieldError{{Field: "name", Message: "required"}})
		return
	}
	if strings.TrimSpace(req.RRule) == "" {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Required fields missing", "",
			[]FieldError{{Field: "rrule", Message: "required"}})
		return
	}
	if _, err := rrule.ValidateRRule(req.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}
	if fe := validateToolConfigFields(req.ToolConfig, "tool_config"); len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid tool_config", "", fe)
		return
	}

	timezone := req.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	tmpl := model.Template{
		Name:              req.Name,
		Description:       req.Description,
		RRule:             req.RRule,
		Timezone:          timezone,
		ToolConfig:        req.ToolConfig,
		MaintenanceWindow: req.MaintenanceWindow,
	}
	if err := h.deps.TemplateStore.Create(r.Context(), &tmpl); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			writeProblem(w, http.StatusConflict, "/problems/already-exists",
				"Template name taken", "", nil)
			return
		}
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Create failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, toTemplateResponse(tmpl))
}

func (h *handlers) updateTemplate(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	cur, err := h.deps.TemplateStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Template not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading template failed", err.Error(), nil)
		return
	}

	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.RRule != nil {
		cur.RRule = *req.RRule
	}
	if req.Timezone != nil {
		cur.Timezone = *req.Timezone
	}
	if req.ToolConfig != nil {
		cur.ToolConfig = req.ToolConfig
	}
	if req.ClearMaintenanceWindow {
		cur.MaintenanceWindow = nil
	} else if req.MaintenanceWindow != nil {
		cur.MaintenanceWindow = req.MaintenanceWindow
	}

	if _, err := rrule.ValidateRRule(cur.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}
	if fe := validateToolConfigFields(cur.ToolConfig, "tool_config"); len(fe) > 0 {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid tool_config", "", fe)
		return
	}

	if err := h.deps.TemplateStore.Update(r.Context(), cur); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Update failed", err.Error(), nil)
		return
	}

	// Cascade: refresh next_run_at for every schedule pointing at this
	// template so the recomputed rrule takes effect on the next tick.
	if h.deps.Expander != nil {
		_, _ = intervalsched.RecomputeNextRunForTemplate(r.Context(), cur.ID,
			h.deps.ScheduleStore, h.deps.TemplateStore, h.deps.Expander)
	}
	writeJSON(w, http.StatusOK, toTemplateResponse(*cur))
}

// deleteTemplate refuses to delete a template that any schedule still
// references unless ?force=true, in which case the dependent schedules
// are deleted atomically along with the template.
func (h *handlers) deleteTemplate(w http.ResponseWriter, r *http.Request, id string) {
	dependents, err := h.deps.ScheduleStore.ListByTemplate(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Listing dependents failed", err.Error(), nil)
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if len(dependents) > 0 && !force {
		fe := make([]FieldError, 0, len(dependents))
		for i, s := range dependents {
			if i >= maxDependentIDsInFieldError {
				fe = append(fe, FieldError{
					Field:   "dependents",
					Message: fmt.Sprintf("... and %d more", len(dependents)-i),
				})
				break
			}
			fe = append(fe, FieldError{Field: "dependents", Message: s.ID})
		}
		writeProblem(w, http.StatusConflict, "/problems/template-in-use",
			"Template is still referenced by schedules; pass ?force=true to cascade delete",
			"", fe)
		return
	}

	if force && len(dependents) > 0 {
		// Cascade delete inside a transaction so either everything goes
		// or nothing does.
		err := h.deps.Store.Transact(r.Context(), func(ctx context.Context, ts storage.TxStores) error {
			for _, s := range dependents {
				if err := ts.Schedules.Delete(ctx, s.ID); err != nil {
					return err
				}
			}
			return ts.Templates.Delete(ctx, id)
		})
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "/problems/not-found",
					"Template not found", "", nil)
				return
			}
			writeProblem(w, http.StatusInternalServerError, "/problems/store",
				"Cascade delete failed", err.Error(), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.deps.TemplateStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Template not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Delete failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateToolConfigFields decodes each tool entry into its registered
// params struct. Unknown tool names and malformed payloads produce
// field-level errors pointing at "prefix.toolname".
func validateToolConfigFields(tc model.ToolConfig, prefix string) []FieldError {
	if tc == nil {
		return nil
	}
	var fe []FieldError
	for name, raw := range tc {
		typ, ok := model.RegisteredToolParams[name]
		if !ok {
			fe = append(fe, FieldError{
				Field:   prefix + "." + name,
				Message: "unknown tool",
			})
			continue
		}
		instance := reflectTypeNew(typ)
		if err := json.Unmarshal(raw, instance); err != nil {
			fe = append(fe, FieldError{
				Field:   prefix + "." + name,
				Message: err.Error(),
			})
		}
	}
	return fe
}

func toTemplateResponse(t model.Template) TemplateResponse {
	if t.ToolConfig == nil {
		t.ToolConfig = model.ToolConfig{}
	}
	return TemplateResponse{
		ID:                t.ID,
		Name:              t.Name,
		Description:       t.Description,
		RRule:             t.RRule,
		Timezone:          t.Timezone,
		ToolConfig:        t.ToolConfig,
		MaintenanceWindow: t.MaintenanceWindow,
		IsSystem:          t.IsSystem,
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
	}
}
