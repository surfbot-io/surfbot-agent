package webui

// Tests for the install_hint contract added in PR3. The hint is the
// server's authoritative answer to "what should the user run?" — the UI
// must never invent these strings, so we lock the per-OS branches here.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestBuildInstallHint_NotInstalled(t *testing.T) {
	h := buildInstallHint(false)
	if h == nil {
		t.Fatal("hint should not be nil")
	}
	if h.InstallCommand == "" {
		t.Error("install_command should be set when not installed")
	}
	if h.StartCommand == "" {
		t.Error("start_command should also be set when not installed")
	}
	if h.DocsURL == "" {
		t.Error("docs_url should be set")
	}
	switch runtime.GOOS {
	case "linux":
		if !h.RequiresAdmin {
			t.Error("linux should require admin")
		}
		if !strings.HasPrefix(h.InstallCommand, "sudo ") {
			t.Errorf("linux install_command should start with sudo, got %q", h.InstallCommand)
		}
		if !strings.HasPrefix(h.StartCommand, "sudo ") {
			t.Errorf("linux start_command should start with sudo, got %q", h.StartCommand)
		}
	case "darwin":
		if h.RequiresAdmin {
			t.Error("darwin should not require admin")
		}
		if strings.Contains(h.InstallCommand, "sudo") {
			t.Errorf("darwin install_command must not contain sudo, got %q", h.InstallCommand)
		}
	case "windows":
		if !h.RequiresAdmin {
			t.Error("windows should require admin")
		}
		if strings.Contains(h.InstallCommand, "sudo") {
			t.Errorf("windows install_command must not contain sudo, got %q", h.InstallCommand)
		}
	}
}

func TestBuildInstallHint_Installed(t *testing.T) {
	h := buildInstallHint(true)
	if h.InstallCommand != "" {
		t.Errorf("install_command should be empty when already installed, got %q", h.InstallCommand)
	}
	if h.StartCommand == "" {
		t.Error("start_command should still be set when installed but stopped")
	}
}

func TestAttachInstallHint_OnlyWhenNotRunning(t *testing.T) {
	running := daemonStatusResponse{Installed: true, Running: true}
	attachInstallHint(&running)
	if running.InstallHint != nil {
		t.Error("running daemon should never get an install_hint")
	}

	stopped := daemonStatusResponse{Installed: true, Running: false}
	attachInstallHint(&stopped)
	if stopped.InstallHint == nil {
		t.Fatal("stopped daemon should get an install_hint")
	}
	if stopped.InstallHint.InstallCommand != "" {
		t.Error("stopped (already installed) hint must omit install_command")
	}
	if stopped.InstallHint.StartCommand == "" {
		t.Error("stopped hint must include start_command")
	}

	missing := daemonStatusResponse{Installed: false, Running: false}
	attachInstallHint(&missing)
	if missing.InstallHint == nil || missing.InstallHint.InstallCommand == "" {
		t.Error("not-installed hint must include install_command")
	}
}

func TestHandleDaemonStatus_NotInstalledIncludesHint(t *testing.T) {
	h := &handler{daemon: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rec := httptest.NewRecorder()
	h.handleDaemonStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp daemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Installed || resp.Running {
		t.Errorf("expected installed=false running=false, got %+v", resp)
	}
	if resp.InstallHint == nil {
		t.Fatal("install_hint missing on not-installed response")
	}
	if resp.InstallHint.InstallCommand == "" {
		t.Error("install_hint.install_command is empty")
	}
	if resp.InstallHint.DocsURL == "" {
		t.Error("install_hint.docs_url is empty")
	}
}
