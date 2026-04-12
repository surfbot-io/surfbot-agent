package webui

import (
	"fmt"
	"net/http"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
)

// handleSchedule routes GET and PUT on /api/v1/schedule.
func (h *handler) handleSchedule(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetSchedule(w, r)
	case http.MethodPut:
		h.handlePutSchedule(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleGetSchedule returns the current schedule configuration. It reads
// from the live scheduler when available, falling back to the persisted
// schedule.config.json, then to the DaemonView defaults.
func (h *handler) handleGetSchedule(w http.ResponseWriter, _ *http.Request) {
	if h.daemon == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon not configured")
		return
	}

	// Try persisted config first (authoritative after first PUT).
	if h.daemon.ScheduleConfigStore != nil {
		sc, err := h.daemon.ScheduleConfigStore.Load()
		if err == nil {
			writeJSON(w, http.StatusOK, sc)
			return
		}
	}

	// Fall back to live scheduler config or DaemonView defaults.
	sc := h.buildScheduleConfigFromView()
	writeJSON(w, http.StatusOK, sc)
}

// handlePutSchedule validates and persists the new schedule config, then
// hot-reloads the running scheduler.
func (h *handler) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	if h.daemon == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon not configured")
		return
	}
	if h.daemon.ScheduleConfigStore == nil {
		writeError(w, http.StatusServiceUnavailable, "schedule config store not available")
		return
	}

	var req intervalsched.ScheduleConfig
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate by parsing into an intervalsched.Config.
	icfg, fieldErrors := intervalsched.ParseScheduleConfig(req)
	if len(fieldErrors) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errors": fieldErrors})
		return
	}

	// Validate tools against registry.
	if h.registry != nil && len(req.QuickCheckTools) > 0 {
		for _, name := range req.QuickCheckTools {
			if _, ok := h.registry.GetByName(name); !ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"errors": map[string]string{
						"quick_check_tools": fmt.Sprintf("unknown tool: %s", name),
					},
				})
				return
			}
		}
	}

	// Persist to schedule.config.json (atomic write, 0600).
	if err := h.daemon.ScheduleConfigStore.Save(req); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save schedule config")
		return
	}

	// Update the DaemonView so GET requests reflect the change immediately.
	h.daemon.SchedulerEnabled = req.Enabled
	h.daemon.WindowStart = req.MaintenanceWindow.Start
	h.daemon.WindowEnd = req.MaintenanceWindow.End
	h.daemon.WindowTimezone = req.MaintenanceWindow.Timezone
	if w2, err := buildWindowFromStrings(req.MaintenanceWindow); err == nil {
		h.daemon.Window = w2
	}

	// Hot-reload the live scheduler if available.
	if h.daemon.Scheduler != nil {
		h.daemon.Scheduler.Reload(icfg)
	}

	writeJSON(w, http.StatusOK, req)
}

// buildScheduleConfigFromView constructs a ScheduleConfig from the
// DaemonView fields (populated from config.yaml at startup).
func (h *handler) buildScheduleConfigFromView() intervalsched.ScheduleConfig {
	sc := intervalsched.ScheduleConfig{
		Enabled:            h.daemon.SchedulerEnabled,
		FullScanInterval:   "24h",
		QuickCheckInterval: "1h",
		Jitter:             "5m",
		MaintenanceWindow: intervalsched.ScheduleConfigWindow{
			Enabled:  h.daemon.Window.Enabled,
			Start:    h.daemon.WindowStart,
			End:      h.daemon.WindowEnd,
			Timezone: h.daemon.WindowTimezone,
		},
	}

	// If we have a live scheduler, read actual config from it.
	if h.daemon.Scheduler != nil {
		cfg := h.daemon.Scheduler.Config()
		sc.Enabled = true // scheduler exists means enabled
		sc.FullScanInterval = cfg.FullInterval.String()
		sc.QuickCheckInterval = cfg.QuickInterval.String()
		sc.Jitter = cfg.Jitter.String()
		sc.RunOnStart = cfg.RunOnStart
		sc.QuickCheckTools = cfg.QuickTools
	}
	return sc
}

// buildWindowFromStrings parses the JSON window config into an
// intervalsched.MaintenanceWindow.
func buildWindowFromStrings(w intervalsched.ScheduleConfigWindow) (intervalsched.MaintenanceWindow, error) {
	mw := intervalsched.MaintenanceWindow{Enabled: w.Enabled}
	if !w.Enabled {
		return mw, nil
	}
	start, err := intervalsched.ParseTimeOfDay(w.Start)
	if err != nil {
		return mw, fmt.Errorf("invalid start time: %w", err)
	}
	end, err := intervalsched.ParseTimeOfDay(w.End)
	if err != nil {
		return mw, fmt.Errorf("invalid end time: %w", err)
	}
	loc := time.Local
	if w.Timezone != "" {
		loc, err = time.LoadLocation(w.Timezone)
		if err != nil {
			return mw, fmt.Errorf("invalid timezone: %w", err)
		}
	}
	mw.Start, mw.End, mw.Loc = start, end, loc
	return mw, nil
}
