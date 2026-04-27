package webui

// UI v2 foundation snapshot (issue #34). The redesign sprint relies on
// a small vocabulary of CSS tokens and JS component helpers that later
// PRs build on. A simple string-presence check on the embedded static
// assets catches the common break modes — accidental removal during a
// refactor, a typo on a token name, a forgotten //go:embed match —
// without standing up a JS test runner just for this. When the
// foundation is renamed in a future sprint, update both the symbol
// list and the call sites at the same time.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFoundationCSSTokens(t *testing.T) {
	data, err := staticFS.ReadFile("static/css/style.css")
	require.NoError(t, err)
	css := string(data)

	tokens := []string{
		// Surface scale.
		"--ink-900:", "--ink-800:", "--ink-700:", "--ink-600:",
		// Brand alias.
		"--brand:",
		// Severity (pre-existing, asserted as a regression guard).
		"--sev-critical:", "--sev-high:", "--sev-medium:", "--sev-low:", "--sev-info:",
		// Finding triage status (net new in PR1).
		"--status-open:", "--status-ack:", "--status-resolved:",
		"--status-fp:", "--status-ignored:",
	}
	for _, tok := range tokens {
		assert.True(t, strings.Contains(css, tok),
			"foundation token %q missing from style.css", tok)
	}

	classes := []string{
		".pill", ".pill-dot",
		".sev-pill-critical", ".sev-pill-high", ".sev-pill-medium",
		".sev-pill-low", ".sev-pill-info",
		".status-pill-open", ".status-pill-ack", ".status-pill-resolved",
		".status-pill-fp", ".status-pill-ignored",
		".filter-chip", ".filter-chip-remove",
		".kbd",
		".icon-12", ".icon-14", ".icon-16",
		".slide-over", ".slide-over-backdrop", ".slide-over-header",
		".slide-over-body", ".slide-over-footer", ".slide-over-close",
		".bulk-bar", ".bulk-bar-count", ".bulk-bar-actions",
	}
	for _, cls := range classes {
		assert.True(t, strings.Contains(css, cls),
			"foundation class %q missing from style.css", cls)
	}
}

func TestFoundationComponentHelpers(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/components.js")
	require.NoError(t, err)
	js := string(data)

	// Each helper is checked by the property-name form used in the
	// Components object literal so the test fails if it's renamed or
	// promoted out of the object without intent.
	helpers := []string{
		"severityPill(", "statusPill(", "filterChip(",
		"kbd(", "icon(", "slideOver(", "bulkBar(",
	}
	for _, h := range helpers {
		assert.True(t, strings.Contains(js, h),
			"foundation helper %q missing from components.js", h)
	}

	// The icon registry must list at least the seed glyphs so PR2 can
	// rely on them without re-discovering whether they exist. PR3 #36
	// added `refresh` for the dashboard header — listed here so the
	// next maintainer doesn't strip it during a refactor.
	icons := []string{
		"x:", "search:", "plus:",
		"'chevron-down':", "'chevron-right':",
		"check:", "clock:", "alert:", "copy:",
		"refresh:",
	}
	for _, ic := range icons {
		assert.True(t, strings.Contains(js, ic),
			"foundation icon %q missing from components.js ICONS registry", ic)
	}
}

// PR3 #36 dashboard reframe. The new dashboard.js drops the agent-info
// block and the .card-grid / .score-container / .detail-grid scaffolding
// in favor of KPI buttons and the .dash-* + .kpi-* foundation. These
// guards catch a partial migration where someone accidentally
// re-introduces a legacy class while iterating on the page.

func TestDashboardLegacyArtifactsRemoved(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/pages/dashboard.js")
	require.NoError(t, err)
	js := string(data)

	legacy := []string{
		// IDs / function names removed by PR3.
		"dashboard-run-scan-btn",
		"renderLastScanCard",
		"renderChangesCard",
		"formatAssetTypes",
		// Legacy CSS classes — these still exist in style.css for
		// unmigrated pages, but the dashboard must not reference them.
		"card-grid",
		"score-container",
		"detail-grid",
	}
	for _, s := range legacy {
		assert.False(t, strings.Contains(js, s),
			"legacy artifact %q must not appear in dashboard.js after PR3 #36", s)
	}
}

func TestDashboardUsesFoundationHelpers(t *testing.T) {
	data, err := staticFS.ReadFile("static/js/pages/dashboard.js")
	require.NoError(t, err)
	js := string(data)

	required := []string{
		"Components.statusPill(",
		"Components.icon(",
		"Components.timeAgo(",
		"Components.formatDuration(",
		// New foundation classes the dashboard depends on.
		"dash-grid",
		"kpi-card",
		"activity-list",
	}
	for _, s := range required {
		assert.True(t, strings.Contains(js, s),
			"dashboard.js must use foundation helper/class %q", s)
	}
}

// TestDashboardCSSScaffolding gates the CSS classes the rewritten
// dashboard page hard-codes. Lives next to the JS guard so a single
// failed test makes the missing piece obvious.
func TestDashboardCSSScaffolding(t *testing.T) {
	data, err := staticFS.ReadFile("static/css/style.css")
	require.NoError(t, err)
	css := string(data)

	classes := []string{
		".dash-grid", ".dash-col-3", ".dash-col-4", ".dash-col-8",
		".kpi-card", ".kpi-label", ".kpi-number", ".kpi-stacked-bar",
		".kpi-counts", ".kpi-mini-pill", ".kpi-trend",
		".sparkline", ".sparkline-bar",
		".dash-panel", ".dash-panel-header", ".dash-panel-body",
		".dash-tab", ".activity-item", ".activity-icon",
		".dash-list-item", ".dash-status-dot",
	}
	for _, c := range classes {
		assert.True(t, strings.Contains(css, c),
			"dashboard CSS class %q missing from style.css", c)
	}
}
