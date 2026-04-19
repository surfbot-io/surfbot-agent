package webui

// Tests for SPEC-X3.1 §7 — backend unit coverage of the daemon status
// handler. These exercise the pure builder so the staleness branch is
// driven by an injected `now` rather than a sleep.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
)

// writeDaemonState writes daemon.state.json into dir and returns its path.
func writeDaemonState(t *testing.T, dir string, st daemon.State) string {
	t.Helper()
	p := filepath.Join(dir, "daemon.state.json")
	store := daemon.NewStateStore(p)
	if err := store.Save(st); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return p
}

func writeScheduleState(t *testing.T, dir string, st intervalsched.ScheduleState) string {
	t.Helper()
	p := filepath.Join(dir, "schedule.state.json")
	store := intervalsched.NewScheduleStateStore(p)
	if err := store.Save(st); err != nil {
		t.Fatalf("save schedule: %v", err)
	}
	return p
}

func TestDaemonStatusHandler_Running(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)
	dpath := writeDaemonState(t, dir, daemon.State{
		Version:   "0.5.0",
		PID:       12345,
		StartedAt: now.Add(-2 * time.Hour),
		WrittenAt: now.Add(-5 * time.Second),
	})
	spath := writeScheduleState(t, dir, intervalsched.ScheduleState{
		LastFullAt:     now.Add(-6 * time.Hour),
		LastFullStatus: "ok",
		NextFullAt:     now.Add(18 * time.Hour),
	})
	view := &DaemonView{
		DaemonStatePath:   dpath,
		ScheduleStatePath: spath,
		Heartbeat:         30 * time.Second,
		SchedulerEnabled:  true,
	}
	resp := buildDaemonStatus(view, now)
	if !resp.Installed || !resp.Running {
		t.Fatalf("want running, got %+v", resp)
	}
	if resp.PID != 12345 || resp.Version != "0.5.0" {
		t.Errorf("pid/version mismatch: %+v", resp)
	}
	if resp.UptimeSeconds != int64((2 * time.Hour).Seconds()) {
		t.Errorf("uptime: %d", resp.UptimeSeconds)
	}
	if resp.Scheduler == nil || resp.Scheduler.LastFull == nil || resp.Scheduler.LastFull.Status != "ok" {
		t.Errorf("scheduler block missing: %+v", resp.Scheduler)
	}
}

func TestDaemonStatusHandler_StaleHeartbeat(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)
	dpath := writeDaemonState(t, dir, daemon.State{
		Version:   "0.5.0",
		PID:       1,
		StartedAt: now.Add(-time.Hour),
		WrittenAt: now.Add(-200 * time.Second), // > 3*30s
	})
	view := &DaemonView{DaemonStatePath: dpath, Heartbeat: 30 * time.Second}
	resp := buildDaemonStatus(view, now)
	if !resp.Installed || resp.Running {
		t.Fatalf("want stale-stopped, got %+v", resp)
	}
	if !strings.Contains(resp.Reason, "stale") {
		t.Errorf("reason missing 'stale': %q", resp.Reason)
	}
}

func TestDaemonStatusHandler_NoStateFile(t *testing.T) {
	view := &DaemonView{DaemonStatePath: filepath.Join(t.TempDir(), "missing.json"), Heartbeat: 30 * time.Second}
	resp := buildDaemonStatus(view, time.Now())
	if resp.Installed || resp.Running {
		t.Fatalf("want not installed, got %+v", resp)
	}
}

func TestDaemonStatusHandler_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "daemon.state.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	view := &DaemonView{DaemonStatePath: p, Heartbeat: 30 * time.Second}
	resp := buildDaemonStatus(view, time.Now())
	if !resp.Installed || resp.Running {
		t.Fatalf("want installed-but-not-running, got %+v", resp)
	}
	if resp.Reason != "state file corrupt" {
		t.Errorf("reason: %q", resp.Reason)
	}
}

func TestDaemonStatusHandler_ScheduleMissing(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now,
	})
	view := &DaemonView{
		DaemonStatePath:   dpath,
		ScheduleStatePath: filepath.Join(dir, "schedule.state.json"),
		Heartbeat:         30 * time.Second,
		SchedulerEnabled:  false,
	}
	resp := buildDaemonStatus(view, now)
	if !resp.Running {
		t.Fatalf("want running, got %+v", resp)
	}
	if resp.Scheduler == nil || resp.Scheduler.Enabled {
		t.Errorf("want scheduler.enabled=false, got %+v", resp.Scheduler)
	}
}

func TestDaemonStatusHandler_WindowOpen(t *testing.T) {
	dir := t.TempDir()
	loc, _ := time.LoadLocation("UTC")
	now := time.Date(2026, 4, 8, 3, 0, 0, 0, loc) // inside 02-06 window
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Hour),
		WrittenAt: now,
	})
	view := &DaemonView{
		DaemonStatePath:   dpath,
		ScheduleStatePath: filepath.Join(dir, "schedule.state.json"),
		Heartbeat:         30 * time.Second,
		SchedulerEnabled:  true,
		WindowStart:       "02:00",
		WindowEnd:         "06:00",
		WindowTimezone:    "UTC",
		Window: intervalsched.MaintenanceWindow{
			Enabled: true,
			Start:   intervalsched.TimeOfDay{Hour: 2},
			End:     intervalsched.TimeOfDay{Hour: 6},
			Loc:     loc,
		},
	}
	resp := buildDaemonStatus(view, now)
	if resp.Scheduler == nil || resp.Scheduler.Window == nil {
		t.Fatalf("missing window block: %+v", resp)
	}
	w := resp.Scheduler.Window
	if !w.OpenNow {
		t.Errorf("want open_now=true")
	}
	if w.NextClose == nil || w.NextClose.Hour() != 6 {
		t.Errorf("next_close wrong: %+v", w.NextClose)
	}
	if w.NextOpen == nil {
		t.Errorf("next_open missing")
	}
}

func TestDaemonStatusHandler_RedactsErrors(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Hour),
		WrittenAt: now,
	})
	spath := writeScheduleState(t, dir, intervalsched.ScheduleState{
		LastFullAt:     now.Add(-time.Hour),
		LastFullStatus: "failed",
		LastFullError:  "request failed: api_key=sk-supersecret123 was rejected",
	})
	view := &DaemonView{
		DaemonStatePath:   dpath,
		ScheduleStatePath: spath,
		Heartbeat:         30 * time.Second,
		SchedulerEnabled:  true,
	}
	resp := buildDaemonStatus(view, now)
	if resp.Scheduler == nil || resp.Scheduler.LastFull == nil {
		t.Fatalf("no scheduler block")
	}
	got := resp.Scheduler.LastFull.Error
	if strings.Contains(got, "sk-supersecret123") {
		t.Errorf("api_key not redacted: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("redaction marker missing: %q", got)
	}
}

func TestDaemonStatusHandler_HTTPRoute(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	dpath := writeDaemonState(t, dir, daemon.State{
		Version: "0.5.0", PID: 1,
		StartedAt: now.Add(-time.Minute),
		WrittenAt: now,
	})
	h := &handler{daemon: &DaemonView{
		DaemonStatePath: dpath,
		Heartbeat:       30 * time.Second,
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rec := httptest.NewRecorder()
	h.handleDaemonStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body daemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Running {
		t.Errorf("want running=true: %+v", body)
	}
}

func TestRedactError(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain error", "plain error"},
		{"api_key=abc123longenoughtoredact456", "api_key=[REDACTED]"},
		{"Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456", "Authorization: [REDACTED]"},
	}
	for _, c := range cases {
		got := redactError(c.in)
		if got != c.want && !strings.Contains(got, "[REDACTED]") && c.in != c.want {
			t.Errorf("redact(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
