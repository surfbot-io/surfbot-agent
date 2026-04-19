package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// BulkOperation enumerates the atomic operations the bulk endpoint
// supports. Pause/resume/delete edit existing rows; clone creates new
// rows from existing ones.
type BulkOperation string

const (
	BulkPause   BulkOperation = "pause"
	BulkResume  BulkOperation = "resume"
	BulkDelete  BulkOperation = "delete"
	BulkClone   BulkOperation = "clone"
)

// BulkScheduleRequest is the POST /api/v1/schedules/bulk body.
//
// ScheduleIDs names the rows to act on. For clone, CreateTemplate
// supplies the name/rrule/dtstart/timezone of the new schedules; each
// source schedule's target_id and tool_config carry over unchanged.
type BulkScheduleRequest struct {
	Operation      BulkOperation          `json:"operation"`
	ScheduleIDs    []string               `json:"schedule_ids"`
	CreateTemplate *CreateScheduleRequest `json:"create_template,omitempty"`
}

// BulkItemError reports a per-item failure from a bulk op.
type BulkItemError struct {
	ScheduleID string `json:"schedule_id"`
	Error      string `json:"error"`
}

// BulkScheduleResponse is the success body. Succeeded holds the IDs
// that completed; Failed reports per-item failures (e.g. id not
// found). The tx commits if every existing-id operation succeeds —
// not-found entries become per-item failures rather than transaction
// aborts.
type BulkScheduleResponse struct {
	Operation BulkOperation   `json:"operation"`
	Succeeded []string        `json:"succeeded"`
	Failed    []BulkItemError `json:"failed"`
}

func (h *handlers) routeBulkSchedules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req BulkScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	if len(req.ScheduleIDs) == 0 {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Empty schedule_ids", "", nil)
		return
	}

	switch req.Operation {
	case BulkPause, BulkResume, BulkDelete, BulkClone:
	default:
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Unknown operation", "must be pause|resume|delete|clone", nil)
		return
	}

	if req.Operation == BulkClone && req.CreateTemplate == nil {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"create_template required for clone", "", nil)
		return
	}

	// Short-circuit clone-level validation before starting the tx so
	// bad inputs don't poison partial work.
	if req.Operation == BulkClone {
		if _, err := rrule.ValidateRRule(req.CreateTemplate.RRule); err != nil {
			writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
				"Invalid RRULE", err.Error(),
				[]FieldError{{Field: "create_template.rrule", Message: err.Error()}})
			return
		}
	}

	succeeded := []string{}
	failed := []BulkItemError{}
	affectedTemplates := map[string]bool{}

	err := h.deps.Store.Transact(r.Context(), func(ctx context.Context, ts storage.TxStores) error {
		for _, id := range req.ScheduleIDs {
			var ferr error
			switch req.Operation {
			case BulkPause:
				ferr = bulkToggle(ctx, ts.Schedules, id, false, &affectedTemplates)
			case BulkResume:
				ferr = bulkToggle(ctx, ts.Schedules, id, true, &affectedTemplates)
			case BulkDelete:
				ferr = bulkDelete(ctx, ts.Schedules, id, &affectedTemplates)
			case BulkClone:
				ferr = bulkClone(ctx, ts.Schedules, id, req.CreateTemplate, &affectedTemplates)
			}
			if ferr == nil {
				succeeded = append(succeeded, id)
				continue
			}
			if errors.Is(ferr, storage.ErrNotFound) {
				failed = append(failed, BulkItemError{ScheduleID: id, Error: "not found"})
				continue
			}
			return fmt.Errorf("schedule %s: %w", id, ferr)
		}
		return nil
	})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/bulk-failed",
			"Bulk operation failed, all changes rolled back", err.Error(), nil)
		return
	}

	// Cascade recompute once per distinct affected template.
	if h.deps.Expander != nil {
		for tmplID := range affectedTemplates {
			_, _ = intervalsched.RecomputeNextRunForTemplate(r.Context(), tmplID,
				h.deps.ScheduleStore, h.deps.TemplateStore, h.deps.Expander)
		}
	}

	writeJSON(w, http.StatusOK, BulkScheduleResponse{
		Operation: req.Operation,
		Succeeded: succeeded,
		Failed:    failed,
	})
}

func bulkToggle(ctx context.Context, store storage.ScheduleStore, id string, enabled bool, templates *map[string]bool) error {
	s, err := store.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.TemplateID != nil && *s.TemplateID != "" {
		(*templates)[*s.TemplateID] = true
	}
	if s.Enabled == enabled {
		return nil
	}
	s.Enabled = enabled
	return store.Update(ctx, s)
}

func bulkDelete(ctx context.Context, store storage.ScheduleStore, id string, templates *map[string]bool) error {
	s, err := store.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.TemplateID != nil && *s.TemplateID != "" {
		(*templates)[*s.TemplateID] = true
	}
	return store.Delete(ctx, id)
}

func bulkClone(ctx context.Context, store storage.ScheduleStore, srcID string, tpl *CreateScheduleRequest, templates *map[string]bool) error {
	src, err := store.Get(ctx, srcID)
	if err != nil {
		return err
	}
	clone := model.Schedule{
		TargetID:          src.TargetID,
		Name:              tpl.Name,
		RRule:             tpl.RRule,
		DTStart:           tpl.DTStart,
		Timezone:          tpl.Timezone,
		TemplateID:        src.TemplateID,
		ToolConfig:        src.ToolConfig,
		MaintenanceWindow: src.MaintenanceWindow,
		Enabled:           true,
		Overrides:         mergeOverrides(src.Overrides, tpl.RRule != "", tpl.Timezone != "", src.MaintenanceWindow != nil),
	}
	if clone.Name == "" {
		clone.Name = src.Name + "-clone"
	}
	if clone.Timezone == "" {
		clone.Timezone = src.Timezone
	}
	if clone.RRule == "" {
		clone.RRule = src.RRule
	}
	if clone.TemplateID != nil && *clone.TemplateID != "" {
		(*templates)[*clone.TemplateID] = true
	}
	return store.Create(ctx, &clone)
}
