package webui

import "net/http"

// handleSchedule serves GET/PUT/POST /api/v1/schedule with a structured
// 410 Gone. The singular schedule endpoint is replaced by first-class
// schedules in agent-spec 3.0 (SPEC-SCHED1); the plural
// /api/v1/schedules surface ships in SCHED1.3. The route is retained so
// misdirected clients receive a clean deprecation signal instead of a
// 404; SCHED1.3 removes the route entirely.
func (h *handler) handleSchedule(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/v1/schedules>; rel="successor-version"`)
	writeJSON(w, http.StatusGone, scheduleGoneResponse())
}

func scheduleGoneResponse() map[string]any {
	return map[string]any{
		"error":                       "deprecated",
		"message":                     "/api/v1/schedule has been replaced by first-class schedules. Use /api/v1/schedules (plural) in agent-spec 3.0.",
		"migrated_to":                 "/api/v1/schedules",
		"agent_spec_version_required": "3.0",
	}
}
