package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
	"github.com/surfbot-io/surfbot-agent/internal/webui"
)

// fakeScanRunner satisfies intervalsched.ScanRunner by writing a
// canned Scan row when the master ticker dispatches a job. Mirrors the
// fake in test/e2e/schedule_e2e_test.go so we can prove the SPEC-SCHED2.0
// production webui wiring fires scans without spawning detection
// binaries.
type fakeUIScanRunner struct {
	store *storage.SQLiteStore
	mu    sync.Mutex
	calls atomic.Int32
}

func (r *fakeUIScanRunner) Run(ctx context.Context, scheduleID, targetID string, _ model.EffectiveConfig) (string, error) {
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

// TestUI_SchedulerInProcessFiresScheduledScan asserts the SPEC-SCHED2.0
// happy path: with the production webui middleware chain on top of the
// in-process scheduler, a schedule whose first occurrence is ≤2s away
// dispatches a scan within ~10s, and the resulting Scan row is
// observable via /api/v1/schedules/{id}.
//
// We don't go through cobra's runUI here because runUI hard-loads
// config from ~/.surfbot/config.yaml and resolves real OS paths. The
// test instead drives the same wiring runUI does: NewServer with the
// in-process scheduler as AdHocDispatcher, on the same goroutine the
// production code starts.
func TestUI_SchedulerInProcessFiresScheduledScan(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; up to ~70s wallclock for the first MINUTELY firing")
	}
	deadline := time.Now().Add(90 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ui-int.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	target := &model.Target{Value: "ui-integration.test.local"}
	require.NoError(t, store.CreateTarget(ctx, target))

	runner := &fakeUIScanRunner{store: store}
	sched, err := intervalsched.New(intervalsched.Dependencies{
		SchedStore:    store.Schedules(),
		TmplStore:     store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
		Runner:        runner,
		Clock:         intervalsched.NewRealClock(),
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

	// daemon.Runner exercise: we want the supervisor + state-file path
	// runUI takes, including SCHED2.0's panic-recover.
	stateStore := daemon.NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	logger := daemon.NewLogger(filepath.Join(t.TempDir(), "test.log"), daemon.LoggerOptions{MaxSizeMB: 1})
	t.Cleanup(func() { _ = logger.Close() })
	daemonRunner := daemon.NewRunner(daemon.RunnerConfig{
		// Wrap the started scheduler in a noop adapter — we already
		// called Start; we just want the heartbeat + state-file write.
		Scheduler: noopSchedulerWrapper{inner: sched},
		State:     stateStore,
		Logger:    logger,
		Heartbeat: 50 * time.Millisecond,
		Version:   "test",
	})
	require.NoError(t, daemonRunner.Start())
	t.Cleanup(func() { _ = daemonRunner.Stop(2 * time.Second) })

	// Pick a free loopback port so allowedHosts/Origins line up.
	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmpLn.Addr().(*net.TCPAddr).Port
	require.NoError(t, tmpLn.Close())

	srv, ln, err := webui.NewServer(store, webui.ServerOptions{
		Bind:    "127.0.0.1",
		Port:    port,
		Version: "test",
		// Production-style DaemonView with the scheduler wired as the
		// ad-hoc dispatcher — this is the SPEC-SCHED2.0 invariant.
		Daemon: &webui.DaemonView{
			DaemonStatePath: stateStore.Path(),
			Heartbeat:       50 * time.Millisecond,
			AdHocDispatcher: sched,
		},
		AuthToken: "ui-int-token",
	})
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	hdrs := map[string]string{
		"Authorization": "Bearer ui-int-token",
		"Origin":        base,
	}

	// Wait for the server to be ready — the auth-protected GET returns
	// 200 with items=[] on an empty schedule table.
	requireReady(t, base, hdrs)

	// Step 1: template
	tplBody := map[string]any{
		"name":     "ui-int-tpl",
		"rrule":    "FREQ=MINUTELY",
		"timezone": "UTC",
		"tool_config": map[string]any{
			"nuclei": model.DefaultNucleiParams(),
		},
	}
	var tpl struct {
		ID string `json:"id"`
	}
	httpJSON(t, http.MethodPost, base+"/api/v1/templates", tplBody, hdrs, http.StatusCreated, &tpl)
	require.NotEmpty(t, tpl.ID)

	// Step 2: schedule with dtstart 59s ago so the first MINUTELY
	// occurrence is ≤1s out.
	dtstart := time.Now().UTC().Add(-59 * time.Second)
	schedBody := map[string]any{
		"target_id":   target.ID,
		"template_id": tpl.ID,
		"name":        "ui-int-sched",
		"rrule":       "FREQ=MINUTELY",
		"dtstart":     dtstart.Format(time.RFC3339Nano),
		"timezone":    "UTC",
	}
	var schedResp struct {
		ID            string     `json:"id"`
		NextRunAt     *time.Time `json:"next_run_at"`
		LastRunStatus *string    `json:"last_run_status"`
		LastScanID    *string    `json:"last_scan_id"`
	}
	httpJSON(t, http.MethodPost, base+"/api/v1/schedules", schedBody, hdrs, http.StatusCreated, &schedResp)
	require.NotEmpty(t, schedResp.ID)
	require.NotNil(t, schedResp.NextRunAt, "Expander must populate next_run_at — UI wiring is broken without it")

	// Step 3: poll until LastRunStatus is success. The first MINUTELY
	// occurrence after a 59s-old DTSTART is ~1s into the next wall-clock
	// minute, so the budget allows for a worst case of ~70s.
	pollDeadline := time.Now().Add(75 * time.Second)
	for {
		if time.Now().After(pollDeadline) {
			t.Fatalf("schedule never fired in-process; runner.calls=%d schedResp=%+v",
				runner.calls.Load(), schedResp)
		}
		time.Sleep(250 * time.Millisecond)
		var cur struct {
			LastRunStatus *string `json:"last_run_status"`
			LastScanID    *string `json:"last_scan_id"`
		}
		httpJSON(t, http.MethodGet, base+"/api/v1/schedules/"+schedResp.ID, nil, hdrs, http.StatusOK, &cur)
		if cur.LastRunStatus != nil && *cur.LastRunStatus == string(model.ScheduleRunSuccess) {
			require.NotNil(t, cur.LastScanID)
			t.Logf("ui-integration: schedule %s fired in %s; scan_id=%s",
				schedResp.ID, time.Since(dtstart).Round(time.Second), *cur.LastScanID)
			return
		}
	}
}

// noopSchedulerWrapper lets us hand an already-Started scheduler to
// daemon.Runner without re-starting it (Start is idempotent on
// *intervalsched.Scheduler, but we want the Runner to NOT call
// Stop on the inner scheduler from its own ctx — Stop is driven by
// the test cleanup).
type noopSchedulerWrapper struct {
	inner *intervalsched.Scheduler
}

func (n noopSchedulerWrapper) Next() time.Time { return n.inner.Next() }
func (n noopSchedulerWrapper) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func requireReady(t *testing.T, base string, hdrs map[string]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/schedules", nil)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server never became ready at %s", base)
}

func httpJSON(t *testing.T, method, url string, body any, hdrs map[string]string, wantStatus int, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, respBody)
	}
	if out != nil {
		require.NoError(t, json.Unmarshal(respBody, out))
	}
}
