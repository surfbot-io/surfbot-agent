package v1

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// stubDispatcher satisfies the Dispatcher interface for tests without
// pulling in the full scheduler + worker pool.
type stubDispatcher struct {
	scanID string
	err    error
	calls  int32
}

func (s *stubDispatcher) DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (string, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.scanID, s.err
}

func TestAdHocSuccess(t *testing.T) {
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	disp := &stubDispatcher{scanID: "scan-abc"}
	deps := defaultAPIDeps(store)
	deps.Dispatcher = disp
	srv := newTestAPI(t, deps)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID:    targetID,
		RequestedBy: "cli",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var out CreateAdHocResponse
	decode(t, raw, &out)
	if out.ScanID != "scan-abc" || out.AdHocRunID == "" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if atomic.LoadInt32(&disp.calls) != 1 {
		t.Fatalf("dispatcher not called")
	}
}

func TestAdHocNilDispatcher503(t *testing.T) {
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	srv := newTestAPI(t, defaultAPIDeps(store)) // Dispatcher left nil

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID: targetID,
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil dispatcher should 503, got %d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	decode(t, raw, &p)
	if p.Type != "/problems/dispatcher-unreachable" {
		t.Fatalf("wrong problem type: %s", p.Type)
	}

	// The persisted run should be marked failed so audit is coherent.
	runs, err := store.AdHocScanRuns().ListByStatus(t.Context(), model.AdHocFailed)
	if err != nil {
		t.Fatalf("list adhoc: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 failed run after 503, got %d", len(runs))
	}
}

func TestAdHocTargetBusy409(t *testing.T) {
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	deps := defaultAPIDeps(store)
	deps.Dispatcher = &stubDispatcher{err: intervalsched.ErrTargetBusy}
	srv := newTestAPI(t, deps)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID: targetID,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	decode(t, raw, &p)
	if p.Type != "/problems/target-busy" {
		t.Fatalf("wrong problem type: %s", p.Type)
	}
}

func TestAdHocInBlackout409(t *testing.T) {
	store := newTestStore(t)
	targetID := seedTarget(t, store, "example.com")
	deps := defaultAPIDeps(store)
	deps.Dispatcher = &stubDispatcher{err: intervalsched.ErrInBlackout}
	srv := newTestAPI(t, deps)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID: targetID,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	decode(t, raw, &p)
	if p.Type != "/problems/in-blackout" {
		t.Fatalf("wrong problem type: %s", p.Type)
	}
}

func TestAdHocUnknownTargetRejected(t *testing.T) {
	store := newTestStore(t)
	deps := defaultAPIDeps(store)
	deps.Dispatcher = &stubDispatcher{}
	srv := newTestAPI(t, deps)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/scans/ad-hoc", CreateAdHocRequest{
		TargetID: "does-not-exist",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
}

// Ensure errors package is always used.
var _ = errors.Is
