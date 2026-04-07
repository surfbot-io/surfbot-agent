//go:build integration && linux

// Package integration exercises the daemon end-to-end against the real
// systemd service manager. It is gated behind the `integration` build tag
// and the linux GOOS so the unit-test job in CI never tries to install a
// system service. Run with: sudo go test -tags=integration ./test/integration/...
package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const binName = "surfbot-itest"

// requireRoot skips the test when not running as uid 0. systemd install
// requires root and we don't want to fail on developer laptops.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("integration test requires root (sudo)")
	}
}

// buildBinary compiles the surfbot binary into a temp dir so we can run
// the daemon subcommands against the real OS service manager. Repo root is
// two parents up from this file (test/integration/).
func buildBinary(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	out := filepath.Join(tmp, binName)
	cmd := exec.Command("go", "build", "-o", out, "./cmd/surfbot")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, stderr.String())
	}
	return out
}

// writeConfig drops a YAML config into a temp HOME so the daemon picks it
// up via viper's `~/.surfbot/config.yaml` lookup. Returns the HOME path
// the caller must export to the binary's environment.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".surfbot")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644))
	return home
}

func runCmd(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestDaemon_InstallStartStatusStopUninstall(t *testing.T) {
	requireRoot(t)
	bin := buildBinary(t)

	// Always tear down even if a sub-step fails.
	t.Cleanup(func() {
		_, _ = runCmd(t, bin, "daemon", "stop")
		_, _ = runCmd(t, bin, "daemon", "uninstall")
	})

	if out, err := runCmd(t, bin, "daemon", "install"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	if out, err := runCmd(t, bin, "daemon", "start"); err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}

	// Poll status up to 10s for "running".
	deadline := time.Now().Add(10 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		out, _ := runCmd(t, bin, "daemon", "status", "--json")
		lastStatus = out
		var s struct {
			Status string `json:"status"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &s) == nil && s.Status == "running" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	require.Contains(t, lastStatus, `"status": "running"`, "daemon never reached running state: %s", lastStatus)

	if out, err := runCmd(t, bin, "daemon", "stop"); err != nil {
		t.Fatalf("stop failed: %v\n%s", err, out)
	}

	// No orphaned scanner subprocesses.
	out, _ := runCmd(t, "pgrep", "-f", "nuclei|httpx|naabu")
	require.Empty(t, strings.TrimSpace(out), "expected no orphan scanner processes, got: %s", out)

	if out, err := runCmd(t, bin, "daemon", "uninstall"); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
}

// schedulerStatus mirrors the JSON shape printed by `daemon status --json`.
// We only decode the fields the integration tests assert on.
type schedulerStatus struct {
	Status    string `json:"status"`
	Scheduler *struct {
		LastFullAt  time.Time `json:"last_full_at"`
		LastQuickAt time.Time `json:"last_quick_at"`
	} `json:"scheduler"`
}

// TestDaemon_Scheduler_TicksWithin90s installs the daemon with very short
// intervals (1m full, 30s quick, run_on_start=true) and asserts that both
// last_full_at and last_quick_at are populated within 90s.
func TestDaemon_Scheduler_TicksWithin90s(t *testing.T) {
	requireRoot(t)
	bin := buildBinary(t)
	home := writeConfig(t, `
daemon:
  scheduler:
    enabled: true
    full_scan_interval: 1m
    quick_check_interval: 30s
    jitter: 0s
    run_on_start: true
    quick_check_tools: [httpx, nuclei]
`)
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		_, _ = runCmd(t, bin, "daemon", "stop")
		_, _ = runCmd(t, bin, "daemon", "uninstall")
	})

	if out, err := runCmd(t, bin, "daemon", "install"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if out, err := runCmd(t, bin, "daemon", "start"); err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}

	deadline := time.Now().Add(90 * time.Second)
	var last schedulerStatus
	for time.Now().Before(deadline) {
		out, _ := runCmd(t, bin, "daemon", "status", "--json")
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &last)
		if last.Scheduler != nil &&
			!last.Scheduler.LastFullAt.IsZero() &&
			!last.Scheduler.LastQuickAt.IsZero() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("scheduler never populated both cursors within 90s; last=%+v", last)
}

// TestDaemon_Scheduler_MaintenanceWindowSuppresses installs with a window
// covering [now, now+5m] and asserts no scan fires while the window is open.
func TestDaemon_Scheduler_MaintenanceWindowSuppresses(t *testing.T) {
	requireRoot(t)
	bin := buildBinary(t)

	now := time.Now()
	end := now.Add(5 * time.Minute)
	cfgBody := `
daemon:
  scheduler:
    enabled: true
    full_scan_interval: 1m
    quick_check_interval: 30s
    jitter: 0s
    run_on_start: true
    maintenance_window:
      enabled: true
      start: "` + now.Format("15:04") + `"
      end: "` + end.Format("15:04") + `"
      timezone: "UTC"
`
	home := writeConfig(t, cfgBody)
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		_, _ = runCmd(t, bin, "daemon", "stop")
		_, _ = runCmd(t, bin, "daemon", "uninstall")
	})

	if out, err := runCmd(t, bin, "daemon", "install"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if out, err := runCmd(t, bin, "daemon", "start"); err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}

	// Wait 90s and confirm scheduler block has NOT recorded a scan.
	time.Sleep(90 * time.Second)
	out, _ := runCmd(t, bin, "daemon", "status", "--json")
	var s schedulerStatus
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &s))
	if s.Scheduler != nil {
		require.True(t, s.Scheduler.LastFullAt.IsZero(), "full scan must not run inside window")
		require.True(t, s.Scheduler.LastQuickAt.IsZero(), "quick scan must not run inside window")
	}
}
