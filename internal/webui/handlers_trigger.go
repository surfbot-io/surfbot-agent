package webui

// SCHED1.2c — POST /api/daemon/trigger reintroduced as an ad-hoc scan
// dispatcher. The pre-1.2b version wrote a trigger.json flag file that
// the legacy IntervalScheduler polled; that polling went away with the
// scheduler rewrite, leaving the path operational-broken. This handler
// preserves the same path so operator muscle memory is intact, but the
// body shape and lifecycle now flow through ad_hoc_scan_runs and the
// master ticker's DispatchAdHoc method (SCHED1.2c R12).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// triggerRequest is the SCHED1.2c body shape. All fields are optional
// except target_id; tool_config overrides flow through into the resolved
// EffectiveConfig the same way a Schedule's tool_config does.
type triggerRequest struct {
	TargetID   string           `json:"target_id"`
	Reason     string           `json:"reason,omitempty"`
	TemplateID *string          `json:"template_id,omitempty"`
	ToolConfig model.ToolConfig `json:"tool_config,omitempty"`
}

// triggerResponse is the 202 body. scan_id is empty on the immediate
// response — the caller can poll ad_hoc_scan_runs by ad_hoc_run_id to
// discover the scan once dispatch has run on the daemon.
type triggerResponse struct {
	AdHocRunID string `json:"ad_hoc_run_id"`
	ScanID     string `json:"scan_id,omitempty"`
}

// handleDaemonTrigger serves POST /api/daemon/trigger.
//
//	200 → impossible (creates work, never returns 200)
//	202 → ad-hoc run created + dispatched
//	400 → malformed JSON or missing target_id
//	404 → target_id does not exist
//	409 → target busy (ErrTargetBusy from DispatchAdHoc)
//	423 → target inside an active blackout (ErrInBlackout)
//	503 → daemon not installed / scheduler not reachable from this UI process
func (h *handler) handleDaemonTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.daemon == nil || h.daemon.DaemonStatePath == "" {
		writeError(w, http.StatusServiceUnavailable, "daemon not installed")
		return
	}
	if h.daemon.AdHocDispatcher == nil {
		writeError(w, http.StatusServiceUnavailable,
			"ad-hoc dispatcher not available in this process; the master ticker runs only inside `surfbot daemon run`")
		return
	}

	var req triggerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.TargetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}

	ctx := r.Context()
	if h.store != nil {
		if _, err := h.store.GetTarget(ctx, req.TargetID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "target not found")
				return
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("getting target: %v", err))
			return
		}
	}

	run := model.AdHocScanRun{
		ID:          uuid.New().String(),
		TargetID:    req.TargetID,
		TemplateID:  req.TemplateID,
		ToolConfig:  req.ToolConfig,
		InitiatedBy: "api:trigger",
		Reason:      req.Reason,
		Status:      model.AdHocPending,
		RequestedAt: time.Now().UTC(),
	}
	if h.store != nil {
		if err := h.store.AdHocScanRuns().Create(ctx, &run); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("create ad-hoc run: %v", err))
			return
		}
	}

	// Synchronously check the predictable refusal cases (busy / blackout)
	// before returning 202 so the operator gets immediate feedback. The
	// scheduler does the same checks at the top of DispatchAdHoc, but
	// because we're calling DispatchAdHoc in a goroutine after returning
	// 202, the operator wouldn't otherwise see the rejection.
	dispatcher := h.daemon.AdHocDispatcher

	// Fire-and-forget: scan completion lands in ad_hoc_scan_runs.
	go func() {
		dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := dispatcher.DispatchAdHoc(dispatchCtx, run); err != nil {
			log.Printf("[webui] ad-hoc dispatch %s: %v", run.ID, err)
		}
	}()

	writeJSON(w, http.StatusAccepted, triggerResponse{AdHocRunID: run.ID})
}

// triggerFromIntervalSchedErr maps the typed errors from
// intervalsched.DispatchAdHoc onto HTTP status codes. Used by tests
// that want to assert the routing — production is fire-and-forget.
func triggerFromIntervalSchedErr(err error) int {
	switch {
	case errors.Is(err, intervalsched.ErrTargetBusy):
		return http.StatusConflict
	case errors.Is(err, intervalsched.ErrInBlackout):
		return http.StatusLocked
	case err == nil:
		return http.StatusOK
	default:
		return http.StatusInternalServerError
	}
}

// (suppress unused import warnings when tests trim the decoder)
var _ = json.NewDecoder
