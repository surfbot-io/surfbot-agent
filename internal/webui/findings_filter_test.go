package webui

// PR5 #38 — filter chip surface for the findings list. The new chips
// derive from the same query params the legacy <select> dropdowns
// targeted, so a regression that drops one of them on the server side
// would make the chip silently ineffective. These tests exercise each
// supported filter through /api/v1/findings and confirm the handler is
// permissive about unknown enum values (returns 200 with zero rows
// rather than 400, matching the chip UX expectation).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindingsListAcceptsAllFilterParams(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	cases := []struct {
		name string
		q    string
	}{
		{"severity", "/api/v1/findings?severity=critical"},
		{"status", "/api/v1/findings?status=open"},
		{"host", "/api/v1/findings?host=blog.example.com"},
		{"tool", "/api/v1/findings?tool=nuclei"},
		{"template_id", "/api/v1/findings?template_id=wordpress-xmlrpc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.q, nil)
			w := httptest.NewRecorder()
			h.handleFindings(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "unexpected status for %s", tc.q)

			var resp findingsResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotNil(t, resp.Findings)
		})
	}
}

func TestFindingsListInvalidEnumIsPermissive(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	// "banana" is not a valid severity enum value. The handler casts
	// the raw string into model.Severity and lets the storage layer
	// match nothing — that's the contract the chip UI relies on (no
	// 400 surfacing while typing into the chip submenu).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?severity=banana", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, len(resp.Findings))
	assert.Equal(t, 0, resp.Total)
}
