package v1

import (
	"net/http"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// APIDeps bundles everything RegisterRoutes wires into the handlers. The
// struct is plain-typed so callers can audit what is exposed to the API
// layer without following an interface jungle.
//
// Store is the SQLiteStore concrete; it exposes the Transact helper that
// bulk ops use. The individual Store fields are the same objects Store
// returns — supplying them directly keeps test setup terse and lets each
// handler take just the dependency it needs.
type APIDeps struct {
	Store         *storage.SQLiteStore
	ScheduleStore storage.ScheduleStore
	TemplateStore storage.TemplateStore
	BlackoutStore storage.BlackoutStore
	DefaultsStore storage.ScheduleDefaultsStore
	AdHocStore    storage.AdHocScanRunStore

	// Expander and Blackouts power the /schedules/upcoming read path.
	// Nil is valid; that endpoint then returns 503 with an explanatory
	// problem body. Both are owned by the scheduler when available.
	Expander  *intervalsched.RRuleExpander
	Blackouts *intervalsched.BlackoutEvaluator

	// Dispatcher may be nil in non-daemon processes. Endpoints needing
	// it return 503 rather than 404 so the API surface is the same
	// shape regardless of which process serves it.
	Dispatcher Dispatcher
}

// RegisterRoutes installs every /api/v1/... handler onto mux. Subsequent
// phases of SPEC-SCHED1.3a add more handlers — each one extends this
// function. No existing routes are replaced; the API is additive.
func RegisterRoutes(mux *http.ServeMux, deps APIDeps) {
	h := &handlers{deps: deps}

	// Schedules CRUD + pause/resume. Ordered so /upcoming and /bulk
	// (registered below) take longest-prefix precedence.
	mux.HandleFunc("/api/v1/schedules", h.routeSchedules)
	mux.HandleFunc("/api/v1/schedules/", h.routeSchedulesSubtree)
	mux.HandleFunc("/api/v1/schedules/upcoming", h.routeUpcoming)
	mux.HandleFunc("/api/v1/schedules/bulk", h.routeBulkSchedules)

	// Templates CRUD.
	mux.HandleFunc("/api/v1/templates", h.routeTemplates)
	mux.HandleFunc("/api/v1/templates/", h.routeTemplateByID)

	// Blackouts CRUD.
	mux.HandleFunc("/api/v1/blackouts", h.routeBlackouts)
	mux.HandleFunc("/api/v1/blackouts/", h.routeBlackoutByID)

	// Singleton schedule defaults.
	mux.HandleFunc("/api/v1/schedule-defaults", h.routeScheduleDefaults)

	// Canonical ad-hoc dispatch. Sibling endpoint /api/daemon/trigger
	// (owned by the webui package) keeps its legacy shape for 1.3a;
	// 1.3b migrates callers onto this path.
	mux.HandleFunc("/api/v1/scans/ad-hoc", h.routeAdHoc)
}

// handlers is the zero-LOC glue struct that binds APIDeps to every
// route. Having a single receiver keeps the route dispatch functions
// tiny and lets handlers call each other as methods without passing
// deps around.
type handlers struct {
	deps APIDeps
}
