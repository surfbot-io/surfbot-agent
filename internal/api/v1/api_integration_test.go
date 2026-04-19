//go:build integration

package v1

// SCHED1.3a integration test (R10): exercises the REST API surface
// end-to-end against a real SQLite file and a stub dispatcher. No real
// scheduler / worker pool — the stub satisfies the Dispatcher interface
// with enough behavior to validate the wiring.
//
// Run with: go test -tags=integration ./internal/api/v1/... -race -count=1

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// integDispatcher records calls and returns a scripted scan ID. Used
// by the CanonicalAdHocWithStubDispatcher scenario so the API layer's
// handshake with the scheduler can be verified without running one.
type integDispatcher struct {
	scanID string
	err    error
	calls  int32
}

func (d *integDispatcher) DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (string, error) {
	atomic.AddInt32(&d.calls, 1)
	return d.scanID, d.err
}

func TestIntegration_EndToEndCRUD(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	// Create template.
	_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/templates", CreateTemplateRequest{
		Name:  "nightly",
		RRule: "FREQ=DAILY",
	})
	var tmpl TemplateResponse
	decode(t, raw, &tmpl)

	// Create schedule referencing the template.
	_, raw = doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "n", Timezone: "UTC",
		TemplateID: &tmpl.ID,
		RRule:      "FREQ=DAILY",
		DTStart:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	var sched ScheduleResponse
	decode(t, raw, &sched)

	// List sees it.
	_, raw = doJSON(t, srv, http.MethodGet, "/api/v1/schedules", nil)
	var page PaginatedResponse[ScheduleResponse]
	decode(t, raw, &page)
	if page.Total != 1 {
		t.Fatalf("list total=%d", page.Total)
	}

	// Pause; list with status=paused sees it.
	resp, _ := doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+sched.ID+"/pause", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause status=%d", resp.StatusCode)
	}
	_, raw = doJSON(t, srv, http.MethodGet, "/api/v1/schedules?status=paused", nil)
	var paused PaginatedResponse[ScheduleResponse]
	decode(t, raw, &paused)
	if paused.Total != 1 {
		t.Fatalf("paused list total=%d", paused.Total)
	}

	// DELETE then GET 404.
	resp, _ = doJSON(t, srv, http.MethodDelete, "/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", resp.StatusCode)
	}
	resp, _ = doJSON(t, srv, http.MethodGet, "/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete status=%d", resp.StatusCode)
	}
}

func TestIntegration_CanonicalAdHocWithStubDispatcher(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	disp := &integDispatcher{scanID: "scan-42"}
	deps := defaultAPIDeps(store)
	deps.Dispatcher = disp
	srv := newTestAPI(t, deps)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID:    targetID,
		RequestedBy: "integration-test",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var out CreateAdHocResponse
	decode(t, raw, &out)
	if out.ScanID != "scan-42" || out.AdHocRunID == "" {
		t.Fatalf("unexpected: %+v", out)
	}
	if atomic.LoadInt32(&disp.calls) != 1 {
		t.Fatalf("dispatcher not called: %d", disp.calls)
	}

	// The ad_hoc_scan_runs row exists with the persisted initiated_by.
	runs, err := store.AdHocScanRuns().ListByTarget(t.Context(), targetID, 10)
	if err != nil {
		t.Fatalf("list adhoc: %v", err)
	}
	if len(runs) != 1 || runs[0].InitiatedBy != "integration-test" {
		t.Fatalf("persisted run not as expected: %+v", runs)
	}
}

func TestIntegration_AdHocNoDispatcher503(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	srv := newTestAPI(t, defaultAPIDeps(store)) // Dispatcher left nil

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID:    targetID,
		RequestedBy: "integration-test",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil dispatcher should 503, got %d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Type != "/problems/dispatcher-unreachable" {
		t.Fatalf("wrong type: %s", p.Type)
	}

	// The persisted row is marked failed for audit.
	runs, err := store.AdHocScanRuns().ListByStatus(t.Context(), model.AdHocFailed)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 failed run, got %d", len(runs))
	}
}
