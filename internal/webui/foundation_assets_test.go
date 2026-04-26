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
	// rely on them without re-discovering whether they exist.
	icons := []string{
		"x:", "search:", "plus:",
		"'chevron-down':", "'chevron-right':",
		"check:", "clock:", "alert:", "copy:",
	}
	for _, ic := range icons {
		assert.True(t, strings.Contains(js, ic),
			"foundation icon %q missing from components.js ICONS registry", ic)
	}
}
