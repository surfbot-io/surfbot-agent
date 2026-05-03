//go:build integration

package webui

// SPEC-SCHED1.4a R11: integration coverage for the UI scaffold.
//
// The existing webui is a vanilla-JS SPA, so "page renders" is proven by
// two signals the Go side owns end-to-end:
//
//   1. The shell (index.html) is served and contains the four new nav
//      hrefs + the four new <script src="..."> tags; the legacy
//      settings_schedule.js tag is gone.
//   2. The embed.FS bundles the four new JS files and does NOT bundle
//      the deleted settings_schedule.js.
//
// Both are cheap string-level checks but they catch the common break
// modes: forgotten embed directive, sidebar link lost in a merge, or
// regressed //go:embed pattern. The live API endpoints are exercised
// with a seeded DB — two templates, three schedules, one blackout, one
// defaults row — to confirm the SPA has real data to render against.
//
// Run with: go test -tags=integration ./internal/webui/... -race -count=1

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// startIntegrationServer boots a real webui.NewServer on a loopback
// port with a file-backed SQLite store, matching the 1.2c deviation
// (:memory: is per-connection, file backing gives a stable schema view
// to every pool conn). Returns base URL and teardown.
func startIntegrationServer(t *testing.T) (*storage.SQLiteStore, string, func()) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ui.db"))
	require.NoError(t, err)

	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmpLn.Addr().(*net.TCPAddr).Port
	require.NoError(t, tmpLn.Close())

	srv, ln, err := NewServer(store, ServerOptions{
		Bind: "127.0.0.1", Port: port, Version: "integ-test",
	})
	require.NoError(t, err)

	go func() { _ = srv.Serve(ln) }()
	time.Sleep(20 * time.Millisecond)

	base := "http://127.0.0.1:" + strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:")
	_ = base // the addr above has port literal; build cleanly
	base = "http://127.0.0.1:" + itoaTest(port)

	cleanup := func() {
		_ = srv.Shutdown(context.Background())
		_ = store.Close()
	}
	return store, base, cleanup
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// seedUIFixtures populates the DB with: 1 target, 2 templates, 3
// schedules (2 active + 1 paused, one using a template), 1 blackout,
// 1 defaults row. The API's list endpoints must then surface those
// rows so the SPA has realistic shapes to render against.
func seedUIFixtures(t *testing.T, store *storage.SQLiteStore) (targetID string, templateIDs []string, scheduleIDs []string, blackoutID string) {
	t.Helper()
	ctx := t.Context()

	tgt := &model.Target{Value: "example.com"}
	require.NoError(t, store.CreateTarget(ctx, tgt))
	targetID = tgt.ID

	for i, name := range []string{"nightly", "weekly"} {
		tmpl := &model.Template{
			Name:     name,
			RRule:    "FREQ=DAILY",
			Timezone: "UTC",
		}
		if i == 1 {
			tmpl.RRule = "FREQ=WEEKLY"
		}
		require.NoError(t, store.Templates().Create(ctx, tmpl))
		templateIDs = append(templateIDs, tmpl.ID)
	}

	mkSched := func(name string, tmplID *string, enabled bool) string {
		s := &model.Schedule{
			TargetID:   targetID,
			Name:       name,
			RRule:      "FREQ=DAILY",
			DTStart:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Timezone:   "UTC",
			TemplateID: tmplID,
			Enabled:    enabled,
		}
		require.NoError(t, store.Schedules().Create(ctx, s))
		return s.ID
	}
	scheduleIDs = append(scheduleIDs,
		mkSched("sched-a", &templateIDs[0], true),
		mkSched("sched-b", &templateIDs[1], true),
		mkSched("sched-c", nil, false),
	)

	// Pad DTSTART by 1s to avoid the 1.1 blackout-overlap guard
	// (propagated test discipline from prior SCHED1 phases).
	bo := &model.BlackoutWindow{
		Scope:       model.BlackoutScopeGlobal,
		Name:        "maintenance",
		RRule:       "FREQ=WEEKLY;BYDAY=SU",
		DurationSec: 3600,
		Timezone:    "UTC",
		Enabled:     true,
	}
	require.NoError(t, store.Blackouts().Create(ctx, bo))
	blackoutID = bo.ID

	defaults := &model.ScheduleDefaults{
		DefaultRRule:       "FREQ=DAILY;BYHOUR=2",
		DefaultTimezone:    "UTC",
		MaxConcurrentScans: 4,
		RunOnStart:         false,
		JitterSeconds:      60,
	}
	require.NoError(t, store.ScheduleDefaults().Update(ctx, defaults))
	return
}

func getBody(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// TestIntegration_UIScaffold_ShellAndEmbed asserts the SPA shell +
// embed.FS are in the expected shape after the 1.4a additions and
// deletions. Cheap string-level checks — they catch the common break
// modes (dropped //go:embed, lost sidebar link, stale <script> tag).
func TestIntegration_UIScaffold_ShellAndEmbed(t *testing.T) {
	_, base, stop := startIntegrationServer(t)
	defer stop()

	// (1) Shell served at `/`.
	code, body := getBody(t, base+"/")
	assert.Equal(t, http.StatusOK, code, "GET / must serve the SPA shell")
	html := string(body)

	// New nav hrefs are all present.
	for _, href := range []string{
		`href="#/schedules"`,
		`href="#/templates"`,
		`href="#/blackouts"`,
		`href="#/settings/defaults"`,
	} {
		assert.Contains(t, html, href, "shell is missing nav link %s", href)
	}

	// New <script> tags are all present.
	for _, src := range []string{
		`/js/pages/schedules.js`,
		`/js/pages/templates.js`,
		`/js/pages/blackouts.js`,
		`/js/pages/settings_defaults.js`,
	} {
		assert.Contains(t, html, src, "shell is missing script tag for %s", src)
	}

	// R10 deletion proofs: legacy page tag and sidebar link gone.
	assert.NotContains(t, html, "settings_schedule.js",
		"shell still references the deleted legacy page script")
	assert.NotContains(t, html, `href="#/settings/schedule"`,
		"shell still has the legacy sidebar link")

	// (2) embed.FS inventory. The four new files must be bundled, the
	// deleted one must not.
	for _, p := range []string{
		"static/js/pages/schedules.js",
		"static/js/pages/templates.js",
		"static/js/pages/blackouts.js",
		"static/js/pages/settings_defaults.js",
	} {
		_, err := fs.Stat(staticFS, p)
		assert.NoError(t, err, "embed.FS is missing %s", p)
	}
	_, err := fs.Stat(staticFS, "static/js/pages/settings_schedule.js")
	assert.Error(t, err, "embed.FS still bundles the deleted settings_schedule.js")
}

// TestIntegration_UIWriteFlows asserts the 1.4b additions are in the
// embedded SPA bundle and the shell wires the ad-hoc nav button. Every
// assertion is a string-match on the embedded file contents or on the
// served index.html — cheap and stable. Behavior verification lives in
// the operator smoke TODO in the PR body; no JS test runner is added.
func TestIntegration_UIWriteFlows(t *testing.T) {
	_, base, stop := startIntegrationServer(t)
	defer stop()

	code, body := getBody(t, base+"/")
	require.Equal(t, http.StatusOK, code)
	html := string(body)

	// (1) Ad-hoc page module is embedded and wired into the shell.
	_, err := fs.Stat(staticFS, "static/js/pages/adhoc.js")
	assert.NoError(t, err, "embed.FS missing static/js/pages/adhoc.js")
	assert.Contains(t, html, `/js/pages/adhoc.js`, "shell missing adhoc.js <script> tag")

	// (2) "Run scan now" nav button in the sidebar.
	assert.Contains(t, html, `id="nav-adhoc-btn"`, "shell missing Run scan now nav button")
	assert.Contains(t, html, `Run scan now`, "shell missing Run scan now label")

	// (3) components.js carries every 1.4b helper name.
	components := readEmbedded(t, "static/js/components.js")
	for _, name := range []string{
		"formInput", "formSelect", "formTextarea", "formDatetime",
		"rruleField", "rruleAttachBlurCheck",
		"modal", "confirmDialog", "applyFieldErrors", "bulkActionsBar",
	} {
		assert.Contains(t, components, name, "components.js missing %s", name)
	}

	// (4) api.js exposes every 1.4b write method.
	apijs := readEmbedded(t, "static/js/api.js")
	for _, name := range []string{
		"createSchedule", "updateSchedule", "deleteSchedule",
		"pauseSchedule", "resumeSchedule", "bulkSchedules",
		"createTemplate", "updateTemplate", "deleteTemplate",
		"createBlackout", "updateBlackout", "deleteBlackout",
		"updateDefaults", "createAdHocScan",
		// Typed-error mapping for the dispatcher modal.
		"DISPATCHER_UNREACHABLE", "TARGET_BUSY", "IN_BLACKOUT",
	} {
		assert.Contains(t, apijs, name, "api.js missing %s", name)
	}

	// (5) pages/*.js each carry their write-flow entry points.
	schedulesJS := readEmbedded(t, "static/js/pages/schedules.js")
	for _, name := range []string{
		"openCreateForm", "openEditForm",
		"setPauseState", "confirmDelete",
		"runBulk", "confirmBulkDelete",
	} {
		assert.Contains(t, schedulesJS, name, "schedules.js missing %s", name)
	}
	templatesJS := readEmbedded(t, "static/js/pages/templates.js")
	for _, name := range []string{"openCreateForm", "openEditForm", "confirmDelete"} {
		assert.Contains(t, templatesJS, name, "templates.js missing %s", name)
	}
	blackoutsJS := readEmbedded(t, "static/js/pages/blackouts.js")
	for _, name := range []string{"openCreateForm", "openEditForm", "confirmDelete"} {
		assert.Contains(t, blackoutsJS, name, "blackouts.js missing %s", name)
	}
	defaultsJS := readEmbedded(t, "static/js/pages/settings_defaults.js")
	for _, name := range []string{"editTemplate", "bindEditActions", "save"} {
		assert.Contains(t, defaultsJS, name, "settings_defaults.js missing %s", name)
	}
	adhocJS := readEmbedded(t, "static/js/pages/adhoc.js")
	for _, name := range []string{"AdHocPage", "readAdHocForm", "swapToSuccessView"} {
		assert.Contains(t, adhocJS, name, "adhoc.js missing %s", name)
	}
}

// TestIntegration_UISchedulingWiring asserts the 1.4c additions that
// survived the UI v2 redesign: schedules-for-target section, target-
// detail ad-hoc prefill support, dashboard Run scan now button, USED BY
// column header, and the blackout activations placeholder.
//
// PR12 #45 removed the Timeline page (`pages/timeline.js`, `#/timeline`
// route, `Components.timelineRow|timelineEmptySlot|groupByDay`). The
// negative assertions below pin those removals so a future regression
// re-introducing the page would fail this test.
func TestIntegration_UISchedulingWiring(t *testing.T) {
	_, base, stop := startIntegrationServer(t)
	defer stop()

	code, body := getBody(t, base+"/")
	require.Equal(t, http.StatusOK, code)
	html := string(body)

	// PR12 #45: Timeline page is gone — shell must not load it and the
	// embed.FS must not ship it.
	assert.NotContains(t, html, `href="#/timeline"`, "shell still has Timeline nav link")
	assert.NotContains(t, html, `/js/pages/timeline.js`, "shell still has timeline.js <script> tag")
	if _, err := fs.Stat(staticFS, "static/js/pages/timeline.js"); err == nil {
		t.Errorf("embed.FS still contains static/js/pages/timeline.js")
	}

	appJS := readEmbedded(t, "static/js/app.js")
	assert.NotContains(t, appJS, "TimelinePage", "app.js still references TimelinePage")
	assert.NotContains(t, appJS, "page: 'timeline'", "app.js still has timeline route registration")
	// The legacy redirect lives on for one sprint so bookmarks don't
	// break — see app.js routes table.
	assert.Contains(t, appJS, "#\\/timeline", "app.js missing legacy #/timeline redirect")

	// PR12 #45: dashboard AgentCard widget is gone. Only the sidebar
	// compact card remains.
	agentCardJS := readEmbedded(t, "static/js/pages/agent_card.js")
	assert.Contains(t, agentCardJS, "mountCompact", "agent_card.js missing mountCompact")
	assert.NotContains(t, agentCardJS, "mount(el)", "agent_card.js still defines dashboard mount()")
	assert.NotContains(t, agentCardJS, "unmount()", "agent_card.js still defines unmount()")
	assert.NotContains(t, appJS, "AgentCard.unmount", "app.js still calls AgentCard.unmount")

	// PR12 #45: timeline helpers are gone from components.js. Surviving
	// schedule helpers (horizonSelector, targetFilterSelector) stay
	// because they're consumed by the schedule detail "Next firings"
	// panel.
	components := readEmbedded(t, "static/js/components.js")
	for _, name := range []string{"timelineRow", "timelineEmptySlot", "groupByDay"} {
		assert.NotContains(t, components, name, "components.js still defines %s", name)
	}
	for _, name := range []string{"horizonSelector", "targetFilterSelector"} {
		assert.Contains(t, components, name, "components.js missing %s", name)
	}

	// Target-detail schedules section + ad-hoc prefill launcher.
	targetsJS := readEmbedded(t, "static/js/pages/targets.js")
	for _, name := range []string{
		"renderTargetSchedulesSection", "targetSchedulesCard",
		"bindTargetSchedulesActions",
		// 1.4c R4: the detail-page Scan now wires to AdHocPage.open.
		"AdHocPage.open",
	} {
		assert.Contains(t, targetsJS, name, "targets.js missing %s", name)
	}

	// AdHoc modal accepts prefill (R4 signature extension).
	adhocJS := readEmbedded(t, "static/js/pages/adhoc.js")
	for _, name := range []string{"prefillTargetID", "lockTargetID", "adhoc-target-unlock"} {
		assert.Contains(t, adhocJS, name, "adhoc.js missing %s", name)
	}

	// Templates list USED BY column + hydration helper.
	templatesJS := readEmbedded(t, "static/js/pages/templates.js")
	for _, name := range []string{"USED BY", "hydrateUsedByCounts", "USED_BY_SOFT_THRESHOLD", "data-used-by"} {
		assert.Contains(t, templatesJS, name, "templates.js missing %s", name)
	}

	// Blackout activations placeholder.
	blackoutsJS := readEmbedded(t, "static/js/pages/blackouts.js")
	for _, name := range []string{`data-section="blackout-activations-placeholder"`, "Activation preview will be available"} {
		assert.Contains(t, blackoutsJS, name, "blackouts.js missing %s", name)
	}
}

// TestIntegration_SchemasEndpoint_ViaWebui confirms SPEC-SCHED1.5 R3
// is reachable through the same webui HTTP stack the SPA uses, not
// just the bare apiv1 mux the per-package tests run against.
func TestIntegration_SchemasEndpoint_ViaWebui(t *testing.T) {
	_, base, stop := startIntegrationServer(t)
	defer stop()

	code, body := getBody(t, base+"/api/v1/schemas/tools")
	assert.Equal(t, http.StatusOK, code)
	var index struct {
		Tools []string `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(body, &index))
	wantCount := 5
	assert.Len(t, index.Tools, wantCount)

	for _, tool := range index.Tools {
		code, body = getBody(t, base+"/api/v1/schemas/tools/"+tool)
		assert.Equal(t, http.StatusOK, code, "tool %s", tool)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(body, &obj), "tool %s", tool)
		assert.NotEmpty(t, obj["title"], "tool %s missing title", tool)
	}

	code, _ = getBody(t, base+"/api/v1/schemas/tools/nonexistent")
	assert.Equal(t, http.StatusNotFound, code)
}

// TestIntegration_NextRunAt_PopulatedOnCreate is SPEC-SCHED1-HOTFIX R4,
// create-path flavor. Before this hotfix, registerV1Routes never set
// APIDeps.Expander, so POST /api/v1/schedules landed with
// next_run_at = NULL. This test drives a create through the full webui
// HTTP stack (webui.NewServer, not apiv1.mux) via the apiclient, which
// proves that the expander is wired AND that the CLI's Origin header
// derivation threads cleanly through validateOrigin. A stored-row read
// confirms the column is populated synchronously, not just on the
// response body.
func TestIntegration_NextRunAt_PopulatedOnCreate(t *testing.T) {
	store, base, stop := startIntegrationServer(t)
	defer stop()

	ctx := t.Context()
	tgt := &model.Target{Value: "example.com"}
	require.NoError(t, store.CreateTarget(ctx, tgt))

	client := apiclient.New(base)
	enabled := true
	created, err := client.CreateSchedule(ctx, apiclient.CreateScheduleRequest{
		TargetID: tgt.ID,
		Name:     "hotfix-create",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
		Enabled:  &enabled,
	})
	require.NoError(t, err, "POST /api/v1/schedules must succeed through webui stack")
	require.NotNil(t, created.NextRunAt, "response body must carry next_run_at")
	assert.True(t, created.NextRunAt.After(time.Now().Add(-1*time.Minute)),
		"next_run_at must be a concrete near-future timestamp, got %v", created.NextRunAt)

	// The stored row must reflect the same populated timestamp — proves
	// the expander wrote through SetNextRunAt synchronously, not just
	// that the response body computed a value locally.
	got, err := store.Schedules().Get(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.NextRunAt, "stored schedule row must have next_run_at populated")
}

// TestIntegration_NextRunAt_PopulatedOnCascades covers the three other
// paths that depend on APIDeps.Expander:
//   - PUT /api/v1/templates/{id} triggers RecomputeNextRunForTemplate
//   - PUT /api/v1/schedule-defaults cascades across every template
//   - POST /api/v1/schedules/bulk (clone op) recomputes dependents of
//     the affected template
//
// Each assertion reads the stored row directly so we verify the
// write-through, not just the handler return value.
func TestIntegration_NextRunAt_PopulatedOnCascades(t *testing.T) {
	store, base, stop := startIntegrationServer(t)
	defer stop()

	ctx := t.Context()
	tgt := &model.Target{Value: "example.com"}
	require.NoError(t, store.CreateTarget(ctx, tgt))

	// Seed a template and a schedule that references it but has
	// next_run_at = NULL (as every pre-hotfix row would).
	tmpl := &model.Template{Name: "nightly", RRule: "FREQ=DAILY", Timezone: "UTC"}
	require.NoError(t, store.Templates().Create(ctx, tmpl))
	sched := &model.Schedule{
		TargetID:   tgt.ID,
		Name:       "cascade-target",
		RRule:      "FREQ=DAILY",
		DTStart:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Timezone:   "UTC",
		TemplateID: &tmpl.ID,
		Enabled:    true,
	}
	require.NoError(t, store.Schedules().Create(ctx, sched))
	require.Nil(t, sched.NextRunAt, "seed schedule must start without next_run_at")

	client := apiclient.New(base)

	// (1) Template update cascade.
	newRRule := "FREQ=WEEKLY"
	_, err := client.UpdateTemplate(ctx, tmpl.ID, apiclient.UpdateTemplateRequest{
		RRule: &newRRule,
	})
	require.NoError(t, err, "PUT /api/v1/templates/{id} must succeed")

	afterTmpl, err := store.Schedules().Get(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, afterTmpl.NextRunAt,
		"template-update cascade must populate dependent schedule's next_run_at")

	// (2) Defaults PUT cascade. Clear next_run_at first so we prove the
	// PUT repopulates it rather than inheriting the prior value.
	require.NoError(t, store.Schedules().SetNextRunAt(ctx, sched.ID, nil))
	cleared, err := store.Schedules().Get(ctx, sched.ID)
	require.NoError(t, err)
	require.Nil(t, cleared.NextRunAt, "setup: expected nil next_run_at before PUT")

	_, err = client.UpdateDefaults(ctx, apiclient.UpdateScheduleDefaultsRequest{
		DefaultRRule:       "FREQ=DAILY;BYHOUR=3",
		DefaultTimezone:    "UTC",
		MaxConcurrentScans: 4,
		JitterSeconds:      0,
	})
	require.NoError(t, err, "PUT /api/v1/schedule-defaults must succeed")
	afterDefaults, err := store.Schedules().Get(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, afterDefaults.NextRunAt,
		"defaults-update cascade must re-populate dependent schedule's next_run_at")

	// (3) Bulk create path — clone preserves src.TemplateID so the
	// affected template's dependents (including our `sched`) are
	// recomputed after the tx commits. Clear next_run_at on the source
	// first so we prove the bulk endpoint re-populates it via cascade.
	require.NoError(t, store.Schedules().SetNextRunAt(ctx, sched.ID, nil))
	bulk, err := client.BulkSchedules(ctx, apiclient.BulkScheduleRequest{
		Operation:   "clone",
		ScheduleIDs: []string{sched.ID},
		CreateTemplate: &apiclient.CreateScheduleRequest{
			Name:     "clone-of-cascade-target",
			RRule:    "FREQ=DAILY",
			Timezone: "UTC",
		},
	})
	require.NoError(t, err, "POST /api/v1/schedules/bulk clone must succeed")
	require.Len(t, bulk.Succeeded, 1, "clone reports one success")

	afterBulk, err := store.Schedules().Get(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, afterBulk.NextRunAt,
		"bulk-create cascade must re-populate dependent schedule's next_run_at")
}

func readEmbedded(t *testing.T, path string) string {
	t.Helper()
	f, err := staticFS.Open(path)
	require.NoError(t, err, "open embedded %s", path)
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(b)
}

// TestIntegration_UIScaffold_RemovedEndpoints proves R9 and R10 removal
// at the HTTP surface. Without the handler, the SPA fallback catches
// `GET /settings/schedule` and serves the shell (200); `POST
// /api/daemon/trigger` is an /api/ path that is no longer registered
// and must 404 cleanly rather than being swallowed by the SPA fallback.
func TestIntegration_UIScaffold_RemovedEndpoints(t *testing.T) {
	_, base, stop := startIntegrationServer(t)
	defer stop()

	// R9: the former POST /api/daemon/trigger returns 404. The SPA
	// fallback refuses to serve index.html for /api/ paths by design
	// (only /js/, /css/, /img/, /static/, /assets/, /fonts/ prefixes
	// and the root are asset paths; /api/ is unregistered and falls
	// through to http.NotFound).
	req, err := http.NewRequest(http.MethodPost, base+"/api/daemon/trigger", strings.NewReader(`{"profile":"full"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"POST /api/daemon/trigger must 404 after SPEC-SCHED1.4a removal")

	// R10: GET /settings/schedule is handled by the SPA fallback
	// (serves index.html with 200). The shell no longer registers
	// `/^#\/settings\/schedule/` as a client-side route, so the SPA
	// router will render its default "Page not found" empty-state once
	// the hash is applied — acceptable per the user's 1.4a smoke
	// protocol (no Go-side 404 expected here).
	code, _ := getBody(t, base+"/settings/schedule")
	assert.Equal(t, http.StatusOK, code,
		"GET /settings/schedule should fall through to the SPA shell")
}

// TestIntegration_UIScaffold_APIEndpoints exercises the 1.3a list and
// singleton endpoints the SPA pages call on load. Seeded fixtures
// guarantee each list is non-empty so the schemas are tested against
// real data, not zero-value responses.
func TestIntegration_UIScaffold_APIEndpoints(t *testing.T) {
	store, base, stop := startIntegrationServer(t)
	defer stop()
	_, templateIDs, scheduleIDs, blackoutID := seedUIFixtures(t, store)

	// GET /api/v1/schedules — contains every seeded ID.
	code, body := getBody(t, base+"/api/v1/schedules")
	assert.Equal(t, http.StatusOK, code)
	for _, id := range scheduleIDs {
		assert.Contains(t, string(body), id, "schedules list missing %s", id)
	}

	// GET /api/v1/schedules/<id> — detail renders.
	code, body = getBody(t, base+"/api/v1/schedules/"+scheduleIDs[0])
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), scheduleIDs[0])

	// GET /api/v1/templates — both templates surface.
	code, body = getBody(t, base+"/api/v1/templates")
	assert.Equal(t, http.StatusOK, code)
	for _, id := range templateIDs {
		assert.Contains(t, string(body), id, "templates list missing %s", id)
	}

	// GET /api/v1/templates/<id> — detail.
	code, body = getBody(t, base+"/api/v1/templates/"+templateIDs[0])
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), templateIDs[0])

	// GET /api/v1/blackouts — blackout present.
	code, body = getBody(t, base+"/api/v1/blackouts")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), blackoutID)

	// GET /api/v1/schedule-defaults — returns a shape with the
	// expected scalar fields. This is a singleton so we just confirm
	// the JSON parses and carries the saved values.
	code, body = getBody(t, base+"/api/v1/schedule-defaults")
	assert.Equal(t, http.StatusOK, code)
	var defaults map[string]any
	require.NoError(t, json.Unmarshal(body, &defaults))
	assert.Equal(t, "UTC", defaults["default_timezone"])
	assert.EqualValues(t, 4, defaults["max_concurrent_scans"])
}
