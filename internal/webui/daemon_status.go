package webui

// SPEC-X3.1 — Agent status + on-demand trigger endpoints.
//
// These handlers expose daemon and scheduler state to the embedded UI by
// reading the same JSON files the CLI's `daemon status` command consumes.
// They never shell out to systemctl/launchctl; liveness is inferred from
// the heartbeat freshness of daemon.state.json (see §3.3 of the spec).

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
)

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

	// triggerMu serializes write access to the trigger flag file.
	triggerMu sync.Mutex
}

// --- Response shapes ---

type daemonStatusResponse struct {
	Installed     bool                   `json:"installed"`
	Running       bool                   `json:"running"`
	Reason        string                 `json:"reason,omitempty"`
	PID           int                    `json:"pid,omitempty"`
	Version       string                 `json:"version,omitempty"`
	StartedAt     *time.Time             `json:"started_at,omitempty"`
	UptimeSeconds int64                  `json:"uptime_seconds,omitempty"`
	Scheduler     *schedulerStatusBlock  `json:"scheduler,omitempty"`
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
	Enabled    bool       `json:"enabled"`
	Start      string     `json:"start,omitempty"`
	End        string     `json:"end,omitempty"`
	Timezone   string     `json:"timezone,omitempty"`
	OpenNow    bool       `json:"open_now"`
	NextOpen   *time.Time `json:"next_open,omitempty"`
	NextClose  *time.Time `json:"next_close,omitempty"`
}

// --- Handler ---

// handleDaemonStatus serves GET /api/daemon/status. It reads both state
// files, infers liveness from the heartbeat freshness, computes window
// state from the loaded config, and redacts sensitive substrings out of
// any error fields before returning. Always 200 — failure modes are
// expressed in the body.
func (h *handler) handleDaemonStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.daemon == nil || h.daemon.DaemonStatePath == "" {
		writeJSON(w, http.StatusOK, daemonStatusResponse{Installed: false})
		return
	}

	resp := buildDaemonStatus(h.daemon, time.Now())
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
		return &windowBlock{Enabled: false}
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

// --- Trigger handler (PR3) ---

type triggerRequest struct {
	Profile string `json:"profile"`
}

type triggerResponse struct {
	Triggered bool   `json:"triggered"`
	Profile   string `json:"profile"`
	TriggerID string `json:"trigger_id"`
}

type triggerFile struct {
	ID          string    `json:"id"`
	Profile     string    `json:"profile"`
	RequestedAt time.Time `json:"requested_at"`
}

func triggerPath(stateDir string) string {
	return filepath.Join(stateDir, "trigger.json")
}

func triggerProcessingPath(stateDir string) string {
	return filepath.Join(stateDir, "trigger.json.processing")
}

// handleDaemonTrigger serves POST /api/daemon/trigger. It writes a flag
// file the daemon's scheduler loop will pick up on its next idle poll.
// The trigger bypasses the maintenance window (explicit user intent —
// see spec §8.3).
func (h *handler) handleDaemonTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.daemon == nil || h.daemon.DaemonStatePath == "" {
		writeError(w, http.StatusServiceUnavailable, "daemon not installed")
		return
	}

	var req triggerRequest
	_ = readJSON(r, &req) // empty body is fine; default applies below
	profile := req.Profile
	if profile == "" {
		profile = "full"
	}
	if profile != "full" && profile != "quick" {
		writeError(w, http.StatusBadRequest, "profile must be 'full' or 'quick'")
		return
	}

	// Check liveness — refuse triggers when the daemon is stale.
	status := buildDaemonStatus(h.daemon, time.Now())
	if !status.Running {
		writeError(w, http.StatusServiceUnavailable, "daemon not running")
		return
	}

	stateDir := filepath.Dir(h.daemon.DaemonStatePath)
	h.daemon.triggerMu.Lock()
	defer h.daemon.triggerMu.Unlock()

	// 409 if a trigger is already in flight (claimed or queued).
	if _, err := os.Stat(triggerProcessingPath(stateDir)); err == nil {
		writeError(w, http.StatusConflict, "a triggered scan is already running")
		return
	}
	if _, err := os.Stat(triggerPath(stateDir)); err == nil {
		writeError(w, http.StatusConflict, "a trigger is already queued")
		return
	}

	id := fmt.Sprintf("tr_%d", time.Now().UnixNano())
	tf := triggerFile{ID: id, Profile: profile, RequestedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encoding trigger")
		return
	}
	tmp := triggerPath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("[webui] writing trigger tmp: %v", err)
		writeError(w, http.StatusInternalServerError, "writing trigger")
		return
	}
	if err := os.Rename(tmp, triggerPath(stateDir)); err != nil {
		_ = os.Remove(tmp)
		log.Printf("[webui] renaming trigger file: %v", err)
		writeError(w, http.StatusInternalServerError, "writing trigger")
		return
	}

	writeJSON(w, http.StatusAccepted, triggerResponse{
		Triggered: true,
		Profile:   profile,
		TriggerID: id,
	})
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
	errorRedactKVPattern  = regexp.MustCompile(`(?i)(api[_-]?key|apikey|token|password|passwd|secret|authorization|bearer)([=:\s]+)([^\s,;"']+)`)
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
