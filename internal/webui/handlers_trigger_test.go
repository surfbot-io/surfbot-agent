package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// fakeDispatcher records calls to DispatchAdHoc and lets the test pick
// the response shape (success / typed error).
type fakeDispatcher struct {
	mu     sync.Mutex
	calls  []model.AdHocScanRun
	scanID string
	err    error
	wait   chan struct{} // optional: block until closed
}

func (f *fakeDispatcher) DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, run)
	wait := f.wait
	scanID := f.scanID
	err := f.err
	f.mu.Unlock()
	if wait != nil {
		<-wait
	}
	return scanID, err
}

func (f *fakeDispatcher) snapshot() []model.AdHocScanRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.AdHocScanRun, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTriggerHandlerWithStore(t *testing.T, dispatcher AdHocDispatcher) (*handler, *storage.SQLiteStore, string) {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now,
	})
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	h := &handler{
		store: store,
		daemon: &DaemonView{
			DaemonStatePath: dpath,
			Heartbeat:       30 * time.Second,
			AdHocDispatcher: dispatcher,
		},
	}
	return h, store, dpath
}

func TestTriggerHandler_202_HappyPath(t *testing.T) {
	dispatcher := &fakeDispatcher{scanID: "s_xyz"}
	h, store, _ := newTriggerHandlerWithStore(t, dispatcher)

	require.NoError(t, store.CreateTarget(context.Background(), &model.Target{
		ID: "t_abc", Value: "example.com", Enabled: true,
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger",
		strings.NewReader(`{"target_id":"t_abc","reason":"smoke test"}`))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code, "body: %s", rec.Body.String())
	var resp triggerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AdHocRunID)

	// Wait for the goroutine to fire.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && len(dispatcher.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	calls := dispatcher.snapshot()
	require.Len(t, calls, 1, "DispatchAdHoc must be invoked async")
	assert.Equal(t, "t_abc", calls[0].TargetID)
	assert.Equal(t, "smoke test", calls[0].Reason)
	assert.Equal(t, model.AdHocPending, calls[0].Status)
}

func TestTriggerHandler_404_UnknownTarget(t *testing.T) {
	dispatcher := &fakeDispatcher{}
	h, _, _ := newTriggerHandlerWithStore(t, dispatcher)

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger",
		strings.NewReader(`{"target_id":"nope"}`))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, dispatcher.snapshot(), "unknown target must not dispatch")
}

func TestTriggerHandler_400_MissingTargetID(t *testing.T) {
	dispatcher := &fakeDispatcher{}
	h, _, _ := newTriggerHandlerWithStore(t, dispatcher)

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTriggerHandler_400_BadJSON(t *testing.T) {
	dispatcher := &fakeDispatcher{}
	h, _, _ := newTriggerHandlerWithStore(t, dispatcher)

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger", strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTriggerHandler_503_NoDispatcher(t *testing.T) {
	// DaemonView with no AdHocDispatcher — the daemon is in another
	// process and DispatchAdHoc isn't reachable.
	dir := t.TempDir()
	now := time.Now()
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1, StartedAt: now.Add(-time.Minute), WrittenAt: now,
	})
	h := &handler{daemon: &DaemonView{DaemonStatePath: dpath, Heartbeat: 30 * time.Second}}

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger",
		strings.NewReader(`{"target_id":"t1"}`))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestTriggerFromIntervalSchedErr_StatusMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, http.StatusOK},
		{intervalsched.ErrTargetBusy, http.StatusConflict},
		{intervalsched.ErrInBlackout, http.StatusLocked},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, triggerFromIntervalSchedErr(c.err), "err=%v", c.err)
	}
}

func TestTriggerHandler_ForwardsToolConfig(t *testing.T) {
	dispatcher := &fakeDispatcher{scanID: "s_a"}
	h, store, _ := newTriggerHandlerWithStore(t, dispatcher)
	require.NoError(t, store.CreateTarget(context.Background(), &model.Target{
		ID: "t_a", Value: "example.org", Enabled: true,
	}))

	body := `{"target_id":"t_a","tool_config":{"nuclei":{"severity":["critical"]}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && len(dispatcher.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	calls := dispatcher.snapshot()
	require.Len(t, calls, 1)
	raw, ok := calls[0].ToolConfig["nuclei"]
	require.True(t, ok, "ToolConfig.nuclei must be forwarded")
	assert.Contains(t, string(raw), "critical")
}
