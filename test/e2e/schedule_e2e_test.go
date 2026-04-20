//go:build e2e

// Package e2e carries the end-to-end confidence test for SPEC-SCHED1.
// It boots the daemon HTTP stack (via the same apiv1.RegisterRoutes
// entry point the production webui process uses), wires a real
// intervalsched.Scheduler with a fake ScanRunner, creates a template
// and a MINUTELY schedule via the REST API, and waits for a scan row
// to materialize on the Scans endpoint. The whole thing must complete
// within 120s wallclock.
//
// Detection tools are NOT invoked — the ScanRunner is a fake that
// creates Scan rows directly, identical to the pattern in
// internal/daemon/intervalsched/scheduler_integration_test.go. This
// test proves the API → scheduler → store path end-to-end, without
// requiring network egress or external binaries.
//
// Run with: go test -tags=e2e ./test/e2e/... -race -count=1
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/surfbot-io/surfbot-agent/internal/api/v1"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// fakeScanRunner satisfies intervalsched.ScanRunner by creating a
// single Scan row when the master ticker fires. No network I/O, no
// detection tool execution, no nuclei/naabu binaries. Mirrors the
// integScanRunner pattern from the intervalsched integration test.
type fakeScanRunner struct {
	store *storage.SQLiteStore
	mu    sync.Mutex
	calls atomic.Int32
}

func (r *fakeScanRunner) Run(ctx context.Context, scheduleID, targetID string, _ model.EffectiveConfig) (string, error) {
	r.calls.Add(1)
	now := time.Now().UTC()
	scan := &model.Scan{
		TargetID:   targetID,
		Type:       model.ScanTypeFull,
		Status:     model.ScanStatusCompleted,
		StartedAt:  &now,
		FinishedAt: &now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.store.CreateScan(ctx, scan); err != nil {
		return "", err
	}
	return scan.ID, nil
}

func TestE2E_BootAndDispatchScheduledScan(t *testing.T) {
	// Hard 120s cap per SPEC-SCHED1.5 R8. The MINUTELY RRULE needs up to
	// ~60s to cross its first occurrence; the test budget doubles that
	// to absorb worker-pool scheduling jitter + overlap guard recompute.
	deadline := time.Now().Add(120 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "e2e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// Seed a target — the /api/v1/* surface assumes one exists before
	// a schedule can reference it, and there is no target CRUD on the
	// versioned API (targets live on the webui handler). Seeding via
	// storage is faithful to the production bootstrap.
	target := &model.Target{Value: "e2e.test.local"}
	require.NoError(t, store.CreateTarget(ctx, target))

	// Wire the scheduler. The fake runner creates real Scan rows so
	// RecordRun's FK is satisfied; the Expander populates next_run_at
	// at schedule-create time via the API handler's refreshNextRun
	// hook. TickInterval is 500ms so the scheduler reacts within a
	// second of the first firing.
	runner := &fakeScanRunner{store: store}
	sched, err := intervalsched.New(intervalsched.Dependencies{
		SchedStore:    store.Schedules(),
		TmplStore:     store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
		Runner:        runner,
		TickInterval:  500 * time.Millisecond,
		JitterSeed:    1,
	})
	require.NoError(t, err)
	require.NoError(t, sched.Start(ctx))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = sched.Stop(stopCtx)
	})

	// Build a standalone Expander/BlackoutEvaluator for the HTTP handler
	// so POST /api/v1/schedules populates next_run_at at create time.
	// This is a read-only sibling of the scheduler's internal expander —
	// the scheduler keeps its own private instance — so no frozen state
	// is mutated. webui's registerV1Routes doesn't expose an Expander
	// hook in production; extending it is out of scope for 1.5.
	defaults, err := store.ScheduleDefaults().Get(ctx)
	require.NoError(t, err)
	blackoutsEval := intervalsched.NewBlackoutEvaluator(store.Blackouts())
	require.NoError(t, blackoutsEval.Refresh(ctx))
	apiExpander := intervalsched.NewRRuleExpander(
		*defaults, blackoutsEval, intervalsched.NewRealClock(), 1,
	)

	mux := http.NewServeMux()
	apiv1.RegisterRoutes(mux, apiv1.APIDeps{
		Store:         store,
		ScheduleStore: store.Schedules(),
		TemplateStore: store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
		Expander:      apiExpander,
		Blackouts:     blackoutsEval,
		Dispatcher:    sched,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Readiness: a GET /api/v1/schedules against an empty DB should
	// return 200 with items=[]. Poll until it does (≤5s).
	waitReady(t, srv.URL)

	// Step 1: create a template carrying default nuclei params.
	createTpl := map[string]any{
		"name":     "e2e-tpl",
		"rrule":    "FREQ=MINUTELY",
		"timezone": "UTC",
		"tool_config": map[string]any{
			"nuclei": model.DefaultNucleiParams(),
		},
	}
	var tplResp apiv1.TemplateResponse
	postJSON(t, srv.URL+"/api/v1/templates", createTpl, &tplResp, http.StatusCreated)
	require.NotEmpty(t, tplResp.ID)

	// Step 2: create a MINUTELY schedule whose DTSTART is a minute ago
	// so the first firing is ≤1 second from now. Pad by -59s so the
	// second-precision truncation lesson from SCHED1.2c doesn't eat
	// the first occurrence.
	dtstart := time.Now().UTC().Add(-59 * time.Second)
	createSched := map[string]any{
		"target_id":   target.ID,
		"template_id": tplResp.ID,
		"name":        "e2e-sched",
		"rrule":       "FREQ=MINUTELY",
		"dtstart":     dtstart.Format(time.RFC3339Nano),
		"timezone":    "UTC",
	}
	var schedResp apiv1.ScheduleResponse
	postJSON(t, srv.URL+"/api/v1/schedules", createSched, &schedResp, http.StatusCreated)
	require.NotEmpty(t, schedResp.ID)
	require.NotNil(t, schedResp.NextRunAt, "next_run_at must be populated — scheduler Expander wiring is broken")

	// Step 3: poll the master ticker's state via the schedule detail
	// endpoint until LastRunStatus is non-nil. That field is set by
	// RecordRun *after* the fake runner returns — the most reliable
	// end-to-end signal that the full chain fired.
	pollDeadline := time.Now().Add(110 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var final apiv1.ScheduleResponse
	for {
		if time.Now().After(pollDeadline) {
			t.Fatalf("timeout waiting for schedule to fire; runner calls=%d next_run_at=%v last_run_status=%v",
				runner.calls.Load(), final.NextRunAt, final.LastRunStatus)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("context canceled: %v", ctx.Err())
		}
		getJSON(t, srv.URL+"/api/v1/schedules/"+schedResp.ID, &final)
		if final.LastRunStatus != nil && *final.LastRunStatus == model.ScheduleRunSuccess {
			break
		}
	}

	// Step 4: verify the scan row the fake runner created is linked
	// back to our schedule via last_scan_id.
	require.NotNil(t, final.LastScanID, "schedule must reference the dispatched scan")
	require.GreaterOrEqual(t, int(runner.calls.Load()), 1, "fake runner must have been invoked at least once")
	t.Logf("e2e success: schedule %s fired in %s; scan_id=%s", schedResp.ID,
		time.Since(dtstart).Round(time.Second), *final.LastScanID)
}

// waitReady polls /api/v1/schedules until it returns a 2xx (or the
// deadline hits). Shields the test from http.Server startup latency
// without sleeping a fixed amount.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/v1/schedules")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server never became ready at %s", base)
}

func postJSON(t *testing.T, url string, body any, out any, wantStatus int) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s: status=%d want=%d body=%s", url, resp.StatusCode, wantStatus, respBody)
	}
	require.NoError(t, json.Unmarshal(respBody, out))
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("GET %s: status=%d body=%s", url, resp.StatusCode, respBody)
	}
	require.NoError(t, json.Unmarshal(respBody, out))
}

