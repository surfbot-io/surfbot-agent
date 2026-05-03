package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// CreateAdHocRequest is the POST /api/v1/scans/ad-hoc body. TargetID is
// required; RequestedBy populates initiated_by on the persisted row so
// audit trails survive process restarts.
//
// Name is accepted from PR9 #42 onward as an optional human label for
// the run (parity with schedule names). It is currently round-tripped
// through dispatch but not persisted — a follow-up backend issue adds
// the column to ad_hoc_scan_runs. Accepting the field today keeps the
// UI from 400-ing on DisallowUnknownFields.
type CreateAdHocRequest struct {
	TargetID           string                     `json:"target_id"`
	TemplateID         *string                    `json:"template_id,omitempty"`
	ToolConfigOverride map[string]json.RawMessage `json:"tool_config_override,omitempty"`
	RequestedBy        string                     `json:"requested_by,omitempty"`
	Reason             string                     `json:"reason,omitempty"`
	Name               string                     `json:"name,omitempty"`
}

// CreateAdHocResponse is the 202 body. ScanID is empty on the immediate
// response — the caller polls ad_hoc_scan_runs by ad_hoc_run_id to
// discover the scan ID once dispatch completes.
type CreateAdHocResponse struct {
	AdHocRunID string `json:"ad_hoc_run_id"`
	ScanID     string `json:"scan_id,omitempty"`
}

func (h *handlers) routeAdHoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}

	var req CreateAdHocRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.TargetID) == "" {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Required fields missing", "",
			[]FieldError{{Field: "target_id", Message: "required"}})
		return
	}

	// FK existence check: target must be real.
	if h.deps.Store != nil {
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
	}

	// Tool config override validation.
	if len(req.ToolConfigOverride) > 0 {
		tc := model.ToolConfig(req.ToolConfigOverride)
		if fe := validateToolConfigFields(tc, "tool_config_override"); len(fe) > 0 {
			writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
				"Invalid tool_config_override", "", fe)
			return
		}
	}

	initiatedBy := req.RequestedBy
	if initiatedBy == "" {
		initiatedBy = "api:ad-hoc"
	}
	run := model.AdHocScanRun{
		TargetID:    req.TargetID,
		TemplateID:  req.TemplateID,
		ToolConfig:  model.ToolConfig(req.ToolConfigOverride),
		InitiatedBy: initiatedBy,
		Reason:      req.Reason,
		Status:      model.AdHocPending,
		RequestedAt: time.Now().UTC(),
	}
	if h.deps.AdHocStore != nil {
		if err := h.deps.AdHocStore.Create(r.Context(), &run); err != nil {
			writeProblem(w, http.StatusInternalServerError, "/problems/store",
				"Persisting ad-hoc run failed", err.Error(), nil)
			return
		}
	}

	h.dispatchAdHoc(w, r.Context(), run)
}

// dispatchAdHoc shares the typed-error translation between this canonical
// endpoint and any future handler (e.g. bulk ad-hoc). Kept internal so
// the existing `/api/daemon/trigger` stays untouched in 1.3a — that
// endpoint's fire-and-forget semantics differ and will migrate to this
// path in 1.3b.
func (h *handlers) dispatchAdHoc(w http.ResponseWriter, ctx context.Context, run model.AdHocScanRun) {
	if h.deps.Dispatcher == nil {
		// Mirror the 1.2c trigger behavior: the master ticker is not
		// reachable from this process. The row already says pending; flip
		// it to failed so the audit trail is coherent without inventing
		// a new status value.
		if h.deps.AdHocStore != nil {
			_ = h.deps.AdHocStore.UpdateStatus(context.Background(), run.ID, model.AdHocFailed, time.Now().UTC())
		}
		writeProblem(w, http.StatusServiceUnavailable, "/problems/dispatcher-unreachable",
			"Scan dispatcher not reachable from this process",
			"the master ticker runs inside `surfbot daemon run`; ad-hoc dispatch requires the same process",
			nil)
		return
	}

	scanID, err := h.deps.Dispatcher.DispatchAdHoc(ctx, run)
	if err != nil {
		switch {
		case errors.Is(err, intervalsched.ErrTargetBusy):
			writeProblem(w, http.StatusConflict, "/problems/target-busy",
				"Target is currently running another scan", "", nil)
			return
		case errors.Is(err, intervalsched.ErrInBlackout):
			writeProblem(w, http.StatusConflict, "/problems/in-blackout",
				"Target is inside an active blackout window", "", nil)
			return
		default:
			writeProblem(w, http.StatusInternalServerError, "/problems/dispatch-failed",
				"Ad-hoc dispatch failed", err.Error(), nil)
			return
		}
	}

	writeJSON(w, http.StatusAccepted, CreateAdHocResponse{
		AdHocRunID: run.ID,
		ScanID:     scanID,
	})
}
