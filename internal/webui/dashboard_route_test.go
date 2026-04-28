package webui

// PR3 #36 dashboard reframe — the rewritten /dashboard SPA pulls every
// KPI from /api/v1/overview, so dropping a field here would silently
// blank a card on the new layout. This test pins the JSON envelope
// shape against what dashboard.js consumes today; treat a failure as a
// signal to update the page in the same commit, not as license to
// loosen the assertion.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverviewBackwardsCompatForDashboard(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	w := httptest.NewRecorder()
	h.handleOverview(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	// Top-level keys consumed by dashboard.js. score_history is
	// optional and only present when a future PR wires it up.
	required := []string{
		"security_score",
		"findings_by_severity",
		"total_assets",
		"assets_by_type",
		"last_scan",
		"changes_since_last",
	}
	for _, k := range required {
		_, ok := raw[k]
		assert.True(t, ok, "dashboard requires %q in /overview response", k)
	}

	last, ok := raw["last_scan"].(map[string]any)
	require.True(t, ok, "last_scan must be an object after seed")
	for _, k := range []string{"id", "target", "status", "duration_seconds", "finished_at", "target_state", "delta", "work"} {
		_, has := last[k]
		assert.True(t, has, "dashboard reads last_scan.%s", k)
	}
}

// score_history is the optional sparkline source. Today the handler
// doesn't emit it; the test pins that contract so adding it later is a
// deliberate, additive change rather than a stealth schema bump.
func TestOverviewOmitsScoreHistoryUntilWired(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	w := httptest.NewRecorder()
	h.handleOverview(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	_, has := raw["score_history"]
	assert.False(t, has, "score_history should stay absent until P1.1 wires the backend")
}
