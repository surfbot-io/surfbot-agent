//go:build integration && linux

package webui

// SPEC-X3.1 §10–11 — integration test for the embedded UI status +
// trigger flow against a real daemon-state filesystem layout. Marked
// linux-only because the cross-platform staleness behavior is identical
// (the timestamp is wall-clock seconds) and CI runs Linux.
//
// Run with: go test -tags integration ./internal/webui/...

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
)

func TestIntegration_StatusPollAndStaleness(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon.state.json")
	store := daemon.NewStateStore(statePath)

	now := time.Now().UTC()
	if err := store.Save(daemon.State{
		Version: "0.5.0", PID: os.Getpid(),
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	view := &DaemonView{
		DaemonStatePath: statePath,
		Heartbeat:       1 * time.Second, // 3s stale threshold for fast test
	}
	h := &handler{daemon: view}

	// Fresh → running.
	rec := httptest.NewRecorder()
	h.handleDaemonStatus(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil))
	var got daemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Running {
		t.Fatalf("want running, got %+v", got)
	}

	// Backdate written_at; expect stale.
	if err := store.Save(daemon.State{
		Version: "0.5.0", PID: os.Getpid(),
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now.Add(-10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.handleDaemonStatus(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Running {
		t.Errorf("want stale-stopped, got %+v", got)
	}
}

func TestIntegration_TriggerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon.state.json")
	now := time.Now().UTC()
	if err := daemon.NewStateStore(statePath).Save(daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	schedPath := filepath.Join(dir, "schedule.state.json")
	view := &DaemonView{
		DaemonStatePath:   statePath,
		ScheduleStatePath: schedPath,
		Heartbeat:         30 * time.Second,
		SchedulerEnabled:  true,
	}
	h := &handler{daemon: view}

	// Fire the trigger.
	rec := httptest.NewRecorder()
	h.handleDaemonTrigger(rec, httptest.NewRequest(http.MethodPost, "/api/daemon/trigger", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger: %d %s", rec.Code, rec.Body.String())
	}

	// Stand up a real scheduler with a fast trigger poll and a fake
	// scanner. Verify it claims the file and writes a LastTrigger
	// record.
	intervalsched.TriggerPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { intervalsched.TriggerPollInterval = 2 * time.Second })

	scanner := &fakeIntegScanner{}
	store := intervalsched.NewScheduleStateStore(schedPath)
	sched := intervalsched.New(intervalsched.Config{
		FullInterval: time.Hour,
		TriggerDir:   dir,
	}, intervalsched.Options{
		Scanner: scanner, StateStore: store, RandSeed: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go sched.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := store.Load()
		if st.LastTrigger != nil {
			if st.LastTrigger.Status != "ok" {
				t.Fatalf("trigger failed: %+v", st.LastTrigger)
			}
			if scanner.calls != 1 {
				t.Errorf("want 1 scan, got %d", scanner.calls)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("trigger never produced a LastTrigger record")
}

type fakeIntegScanner struct{ calls int }

func (f *fakeIntegScanner) Run(_ context.Context, _ intervalsched.Profile) error {
	f.calls++
	return nil
}
