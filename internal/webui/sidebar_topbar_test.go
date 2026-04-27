package webui

// UI v2 PR2 (#35) — sidebar grouping + topbar invariants. Same
// string-presence approach as foundation_assets_test.go: catches the
// common break modes (a refactor accidentally drops the Findings
// badge, a typo on a group label, the Reports bucket sneaking back in
// from a paste, a forgotten breadcrumb mapping) without standing up a
// JS test runner. When PR3+ migrate the markup, update both the symbol
// list and the call sites at the same time.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSidebarFiveGroupLabels(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)

	groups := []string{"Insights", "Discover", "Detect", "Triage", "Configure"}
	for _, g := range groups {
		marker := `class="nav-group-label">` + g + `<`
		assert.True(t, strings.Contains(html, marker),
			"sidebar group label %q missing or unwrapped from .nav-group-label", g)
	}

	// PR2 acceptance: groups appear in the documented top-down order.
	idx := -1
	for _, g := range groups {
		marker := `class="nav-group-label">` + g + `<`
		next := strings.Index(html, marker)
		require.GreaterOrEqual(t, next, 0, "group %q not found", g)
		assert.Greater(t, next, idx, "group %q is out of order", g)
		idx = next
	}
}

func TestSidebarItemCount(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)

	// Exactly 11 items in the new sidebar:
	//   Insights:  Dashboard
	//   Discover:  Targets, Assets, Changes
	//   Detect:    Scans, Schedules, Templates
	//   Triage:    Findings
	//   Configure: Tools, Blackouts, Settings
	// Counted via data-page= rather than class="nav-link because the
	// latter also matches the .nav-links <ul> wrapper.
	count := strings.Count(html, `data-page="`)
	assert.Equal(t, 11, count, "sidebar must have exactly 11 nav-link entries (got %d)", count)

	// Reports is a cloud-only feature (surfbot-web). It must never appear
	// in the local agent's sidebar — guard against a future paste error.
	assert.False(t, strings.Contains(html, `data-page="reports"`),
		"Reports nav link must not appear in the agent sidebar (cloud-only feature)")
	assert.False(t, strings.Contains(html, `>Reports<`),
		"Reports label must not appear in the agent sidebar")
}

func TestSidebarItemPages(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)

	pages := []string{
		"dashboard", "targets", "assets", "changes",
		"scans", "schedules", "templates",
		"findings",
		"tools", "blackouts", "settings",
	}
	for _, p := range pages {
		marker := `data-page="` + p + `"`
		assert.True(t, strings.Contains(html, marker),
			"sidebar item with data-page=%q missing", p)
	}
}

func TestTopbarMarkup(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)

	// Topbar shell + slots. Each line is a load-bearing hook for app.js
	// (breadcrumbs, scan indicator, button stubs).
	hooks := []string{
		`class="topbar"`,
		`id="breadcrumbs"`,
		`id="scan-indicator"`,
		`id="scan-indicator-phase"`,
		`id="topbar-cmdk-btn"`,
		`id="topbar-new-btn"`,
		`id="topbar-notif-btn"`,
		`id="topbar-help-btn"`,
		`class="kbd"`,
	}
	for _, h := range hooks {
		assert.True(t, strings.Contains(html, h),
			"topbar hook %q missing from index.html", h)
	}

	// Notif and help icons advertise the v0.6 ETA via title=. The
	// acceptance criteria call out this specific copy — keep it
	// consistent so the Tooltip test in any future Playwright pass can
	// rely on it.
	assert.GreaterOrEqual(t,
		strings.Count(html, `title="Coming in v0.6"`), 2,
		"notif and help icons must advertise the v0.6 ETA via title=")
}

func TestSidebarAgentStatusFooter(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)

	// PR2 #35: the agent status moved from the dashboard into a
	// compact footer slot inside the sidebar. The slot id is the
	// hand-off point with AgentCard.mountCompact in app.js.
	hooks := []string{
		`id="agent-status-slot"`,
		`class="agent-status-compact"`,
	}
	for _, h := range hooks {
		assert.True(t, strings.Contains(html, h),
			"sidebar compact agent status hook %q missing", h)
	}
}

func TestTopbarStyles(t *testing.T) {
	data, err := staticFS.ReadFile("static/css/style.css")
	require.NoError(t, err)
	css := string(data)

	classes := []string{
		".nav-group", ".nav-group-label",
		".main-area",
		".topbar", ".topbar-btn", ".topbar-icon-btn",
		".breadcrumbs", ".breadcrumb-item", ".breadcrumb-sep",
		".scan-indicator", ".scan-indicator-dot", ".scan-indicator-phase",
		".agent-status-compact",
	}
	for _, cls := range classes {
		assert.True(t, strings.Contains(css, cls),
			"PR2 style hook %q missing from style.css", cls)
	}

	// Topbar sticks to the top of .main-area. The acceptance criteria
	// call out "Topbar sticky" — keep the rule pinned so a refactor
	// can't quietly drop it.
	stickyBlock := strings.Index(css, ".topbar {")
	require.GreaterOrEqual(t, stickyBlock, 0)
	end := strings.Index(css[stickyBlock:], "}")
	require.Greater(t, end, 0)
	assert.Contains(t, css[stickyBlock:stickyBlock+end], "position: sticky",
		".topbar must keep position: sticky for the PR2 acceptance criteria")
}

func TestAppBreadcrumbMapping(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/app.js")
	require.NoError(t, err)
	js := string(data)

	// The Breadcrumbs.MAP is the source of truth for the topbar crumb
	// trail. Every list-route gets a [group, page] entry — pin them so
	// a typo doesn't break the rendered path.
	entries := []string{
		`'/findings':`,
		`'/assets':`,
		`'/targets':`,
		`'/changes':`,
		`'/scans':`,
		`'/schedules':`,
		`'/templates':`,
		`'/tools':`,
		`'/blackouts':`,
		`'/settings':`,
	}
	for _, e := range entries {
		assert.True(t, strings.Contains(js, e),
			"breadcrumb mapping for %q missing from app.js", e)
	}

	// Acceptance criteria: navigating to /findings yields
	// "Surfbot / Triage / Findings". The mapping entry
	// covers this; the explicit assertion is the human-readable guard.
	assert.True(t, strings.Contains(js, `'Triage',    'Findings'`),
		"breadcrumb mapping for /findings must be ['Triage', 'Findings']")
}

func TestAppScanIndicatorWired(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/app.js")
	require.NoError(t, err)
	js := string(data)

	hooks := []string{
		"ScanIndicator",
		"API.scanStatus()",
		"scan-indicator",
		"scan-indicator-phase",
	}
	for _, h := range hooks {
		assert.True(t, strings.Contains(js, h),
			"ScanIndicator hook %q missing from app.js", h)
	}
}

func TestAgentCardMountCompact(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/pages/agent_card.js")
	require.NoError(t, err)
	js := string(data)

	hooks := []string{
		"mountCompact(",
		"refreshCompact(",
		"compactTemplate(",
	}
	for _, h := range hooks {
		assert.True(t, strings.Contains(js, h),
			"AgentCard compact helper %q missing", h)
	}
}
