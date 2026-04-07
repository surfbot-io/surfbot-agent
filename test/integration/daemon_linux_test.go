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
