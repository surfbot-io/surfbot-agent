package webui

// SPEC-X3.1 — Agent status + on-demand trigger endpoints.
//
// These handlers expose daemon and scheduler state to the embedded UI by
// reading the same JSON files the CLI's `daemon status` command consumes.
// They never shell out to systemctl/launchctl; liveness is inferred from
// the heartbeat freshness of daemon.state.json (see §3.3 of the spec).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// AdHocDispatcher is the narrow surface the trigger handler needs from
// the master ticker. The production type is *intervalsched.Scheduler;
// tests inject a fake.
type AdHocDispatcher interface {
	DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (string, error)
}

// DaemonView bundles everything the UI needs to read agent state. It is
// the boundary between the webui package and the daemon/intervalsched
// packages — the CLI populates it once at `surfbot ui` startup.
//
// All fields are optional. When DaemonStatePath is empty the handlers
// report the daemon as not installed.
type DaemonView struct {
	// DaemonStatePath is the absolute path to daemon.state.json.
	DaemonStatePath string
	// ScheduleStatePath is the absolute path to schedule.state.json.
	ScheduleStatePath string
	// Heartbeat is the configured daemon heartbeat. The status handler
	// reports the daemon as stopped when written_at is older than 3×.
	Heartbeat time.Duration
	// SchedulerEnabled mirrors daemon.scheduler.enabled in config. When
	// false, the response collapses the scheduler block.
	SchedulerEnabled bool
	// Window is the parsed maintenance window (may be disabled). Used to
	// compute open_now/next_open/next_close server-side.
	Window intervalsched.MaintenanceWindow
	// WindowStart/End/Timezone are the raw config strings echoed back to
	// the UI for display.
	WindowStart    string
	WindowEnd      string
	WindowTimezone string

	// AdHocDispatcher is the master ticker's ad-hoc dispatch entry
	// point. When non-nil, /api/v1/scans/ad-hoc creates an
	// ad_hoc_scan_runs row and dispatches via this — when nil, the
	// endpoint returns 503 (the daemon is in a separate process and
	// not reachable from this UI process).
	AdHocDispatcher AdHocDispatcher

	// triggerMu serializes write access to the trigger flag file.
	triggerMu sync.Mutex
}

// --- Response shapes ---

type daemonStatusResponse struct {
	Installed     bool                  `json:"installed"`
	Running       bool                  `json:"running"`
	Reason        string                `json:"reason,omitempty"`
	PID           int                   `json:"pid,omitempty"`
	Version       string                `json:"version,omitempty"`
	StartedAt     *time.Time            `json:"started_at,omitempty"`
	UptimeSeconds int64                 `json:"uptime_seconds,omitempty"`
	Scheduler     *schedulerStatusBlock `json:"scheduler,omitempty"`
	InstallHint   *installHint          `json:"install_hint,omitempty"`
}

// installHint tells the UI exactly which command to surface when the
// daemon is not installed or not running. The server is the source of
// truth: it knows the OS and install mode, the UI does not.
type installHint struct {
	InstallCommand string `json:"install_command,omitempty"`
	StartCommand   string `json:"start_command,omitempty"`
	DocsURL        string `json:"docs_url,omitempty"`
	RequiresAdmin  bool   `json:"requires_admin"`
}

const docsRunAsService = "https://github.com/surfbot-io/surfbot-agent#run-as-a-service"

// buildInstallHint returns the install/start commands for the current OS
// and install mode. Linux defaults to system-mode (sudo), macOS to user-
// mode (no sudo), Windows always to system-mode but the binary cannot
// self-elevate from the CLI — the UI is told via requires_admin so it
// can render an "elevated PowerShell" hint instead of inventing a prefix.
func buildInstallHint(installed bool) *installHint {
	hint := &installHint{DocsURL: docsRunAsService}
	switch runtime.GOOS {
	case "linux":
		hint.RequiresAdmin = true
		hint.InstallCommand = "sudo surfbot daemon install"
		hint.StartCommand = "sudo surfbot daemon start"
	case "windows":
		hint.RequiresAdmin = true
		hint.InstallCommand = "surfbot daemon install"
		hint.StartCommand = "surfbot daemon start"
	default: // darwin and others fall through to user-mode
		hint.RequiresAdmin = false
		hint.InstallCommand = "surfbot daemon install"
		hint.StartCommand = "surfbot daemon start"
	}
	if installed {
		// Already installed: only the start command is meaningful.
		hint.InstallCommand = ""
	}
	return hint
}

type schedulerStatusBlock struct {
	Enabled   bool             `json:"enabled"`
	LastFull  *scanResultBlock `json:"last_full,omitempty"`
	LastQuick *scanResultBlock `json:"last_quick,omitempty"`
	NextFull  *time.Time       `json:"next_full,omitempty"`
	NextQuick *time.Time       `json:"next_quick,omitempty"`
	Window    *windowBlock     `json:"window,omitempty"`
}

type scanResultBlock struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
}

type windowBlock struct {
	Enabled   bool       `json:"enabled"`
	Start     string     `json:"start,omitempty"`
	End       string     `json:"end,omitempty"`
	Timezone  string     `json:"timezone,omitempty"`
	OpenNow   bool       `json:"open_now"`
	NextOpen  *time.Time `json:"next_open,omitempty"`
	NextClose *time.Time `json:"next_close,omitempty"`
}

// --- Handler ---

// handleDaemonStatus serves GET /api/daemon/status. It reads both state
// files, infers liveness from the heartbeat freshness, computes window
// state from the loaded config, and redacts sensitive substrings out of
// any error fields before returning. Always 200 — failure modes are
// expressed in the body.
func (h *handler) handleDaemonStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodHead {
		// HEAD is useful for liveness probes (curl -I, k8s readiness)
		// without parsing JSON. Same status, no body.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	if h.daemon == nil || h.daemon.DaemonStatePath == "" {
		writeJSON(w, http.StatusOK, daemonStatusResponse{
			Installed:   false,
			InstallHint: buildInstallHint(false),
		})
		return
	}

	resp := buildDaemonStatus(h.daemon, time.Now())
	attachInstallHint(&resp)
	writeJSON(w, http.StatusOK, resp)
}

// buildDaemonStatus is the pure function exercised by tests. It takes a
// reference time so the staleness branch is deterministic.
func buildDaemonStatus(d *DaemonView, now time.Time) daemonStatusResponse {
	resp := daemonStatusResponse{Installed: false}

	statePath := d.DaemonStatePath
	if _, err := os.Stat(statePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resp
		}
		// Permission errors etc. — surface as not-running rather than 500.
		resp.Installed = true
		resp.Reason = fmt.Sprintf("state file unreadable: %v", err)
		return resp
	}
	resp.Installed = true

	store := daemon.NewStateStore(statePath)
	st, err := store.Load()
	if err != nil {
		log.Printf("[webui] daemon state file corrupt: %v", err)
		resp.Reason = "state file corrupt"
		return resp
	}

	// Liveness check via written_at + 3× heartbeat. We deliberately do
	// not signal(0) the PID — see spec §3.3 for the tradeoff.
	heartbeat := d.Heartbeat
	if heartbeat <= 0 {
		heartbeat = 30 * time.Second
	}
	stale := 3 * heartbeat
	if !st.WrittenAt.IsZero() && now.Sub(st.WrittenAt) > stale {
		resp.Reason = fmt.Sprintf("state file present but heartbeat stale (>%s)", stale)
		return resp
	}
	if st.WrittenAt.IsZero() {
		// Older daemon binary that does not write WrittenAt yet. Trust
		// the file's mtime as a fallback so the upgrade path works.
		if info, ierr := os.Stat(statePath); ierr == nil {
			if now.Sub(info.ModTime()) > stale {
				resp.Reason = fmt.Sprintf("state file mtime stale (>%s)", stale)
				return resp
			}
		}
	}

	resp.Running = true
	resp.PID = st.PID
	resp.Version = st.Version
	if !st.StartedAt.IsZero() {
		started := st.StartedAt
		resp.StartedAt = &started
		resp.UptimeSeconds = int64(now.Sub(started).Seconds())
		if resp.UptimeSeconds < 0 {
			resp.UptimeSeconds = 0
		}
	}

	resp.Scheduler = buildSchedulerBlock(d, now)
	return resp
}

// attachInstallHint fills in the install_hint block on responses where
// the daemon is missing or stopped. Kept separate from buildDaemonStatus
// so the existing tests on the pure status payload don't have to deal
// with the OS-dependent string.
func attachInstallHint(resp *daemonStatusResponse) {
	if resp.Running {
		return
	}
	resp.InstallHint = buildInstallHint(resp.Installed)
}

func buildSchedulerBlock(d *DaemonView, now time.Time) *schedulerStatusBlock {
	if d.ScheduleStatePath == "" {
		return nil
	}
	if _, err := os.Stat(d.ScheduleStatePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !d.SchedulerEnabled {
				return &schedulerStatusBlock{Enabled: false, Window: buildWindowBlock(d, now)}
			}
			// Scheduler enabled but no state yet — first tick has not run.
			return &schedulerStatusBlock{Enabled: true, Window: buildWindowBlock(d, now)}
		}
		log.Printf("[webui] schedule state file unreadable: %v", err)
		return nil
	}

	store := intervalsched.NewScheduleStateStore(d.ScheduleStatePath)
	st, err := store.Load()
	if err != nil {
		log.Printf("[webui] schedule state file corrupt: %v", err)
		return &schedulerStatusBlock{Enabled: d.SchedulerEnabled, Window: buildWindowBlock(d, now)}
	}

	block := &schedulerStatusBlock{
		Enabled: d.SchedulerEnabled,
		Window:  buildWindowBlock(d, now),
	}
	if !st.LastFullAt.IsZero() {
		block.LastFull = &scanResultBlock{
			At:     st.LastFullAt,
			Status: nonEmpty(st.LastFullStatus, "ok"),
			Error:  redactError(st.LastFullError),
		}
	}
	if !st.LastQuickAt.IsZero() {
		block.LastQuick = &scanResultBlock{
			At:     st.LastQuickAt,
			Status: nonEmpty(st.LastQuickStatus, "ok"),
			Error:  redactError(st.LastQuickError),
		}
	}
	if !st.NextFullAt.IsZero() {
		t := st.NextFullAt
		block.NextFull = &t
	}
	if !st.NextQuickAt.IsZero() {
		t := st.NextQuickAt
		block.NextQuick = &t
	}
	return block
}

func buildWindowBlock(d *DaemonView, now time.Time) *windowBlock {
	if !d.Window.Enabled {
		return &windowBlock{
			Enabled:  false,
			Start:    d.WindowStart,
			End:      d.WindowEnd,
			Timezone: d.WindowTimezone,
		}
	}
	wb := &windowBlock{
		Enabled:  true,
		Start:    d.WindowStart,
		End:      d.WindowEnd,
		Timezone: d.WindowTimezone,
		OpenNow:  d.Window.Contains(now),
	}
	// "Open" in spec §2 means "scans are blocked" (the window is active).
	// Compute next_close = next time the active window ends, and
	// next_open = next time it starts.
	if wb.OpenNow {
		closeAt := d.Window.NextOpen(now)
		if !closeAt.IsZero() {
			wb.NextClose = &closeAt
		}
	}
	if next := nextWindowStart(d.Window, now); !next.IsZero() {
		wb.NextOpen = &next
	}
	return wb
}

// nextWindowStart returns the next moment when the maintenance window
// becomes active, scanning forward day-by-day. Cheap because the search
// space is at most 1 day in the future.
func nextWindowStart(w intervalsched.MaintenanceWindow, after time.Time) time.Time {
	if !w.Enabled {
		return time.Time{}
	}
	loc := w.Loc
	if loc == nil {
		loc = time.Local
	}
	local := after.In(loc)
	for offset := 0; offset <= 1; offset++ {
		candidate := time.Date(local.Year(), local.Month(), local.Day()+offset,
			w.Start.Hour, w.Start.Minute, 0, 0, loc)
		if candidate.After(after) {
			return candidate
		}
	}
	// Should be unreachable: in 24h we always cross the start.
	return time.Time{}
}

// --- helpers ---

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// errorRedactPattern catches the most common credential leak shapes:
// `key=value`, `Authorization: Bearer xxx`, and bare hex/jwt-looking
// blobs longer than 24 chars. The list is intentionally conservative —
// the X1 logger should be doing the heavy lifting; this is a second
// line of defense before exposing errors over HTTP.
var (
	errorRedactKVPattern   = regexp.MustCompile(`(?i)(api[_-]?key|apikey|token|password|passwd|secret|authorization|bearer)([=:\s]+)([^\s,;"']+)`)
	errorRedactBlobPattern = regexp.MustCompile(`\b[A-Za-z0-9_\-]{32,}\b`)
)

func redactError(s string) string {
	if s == "" {
		return ""
	}
	out := errorRedactKVPattern.ReplaceAllString(s, "${1}${2}[REDACTED]")
	out = errorRedactBlobPattern.ReplaceAllString(out, "[REDACTED]")
	if len(out) > 200 {
		out = out[:200] + "…"
	}
	return out
}
