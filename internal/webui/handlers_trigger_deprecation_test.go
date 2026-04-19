package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTriggerHandler_DeprecationHeaders verifies that every response
// (success, client error, 503) carries the RFC-8594 Deprecation/Sunset
// signals and the RFC-8288 Link pointing at the successor endpoint.
// The header set is the machine-readable contract 1.4 relies on to
// migrate callers before removal.
func TestTriggerHandler_DeprecationHeaders(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T) (*handler, string)
		body   string
		status int
	}{
		{
			name: "503_no_dispatcher",
			setup: func(t *testing.T) (*handler, string) {
				h := &handler{daemon: &DaemonView{DaemonStatePath: "/nonexistent"}}
				return h, ""
			},
			body:   `{"target_id":"t1"}`,
			status: http.StatusServiceUnavailable,
		},
		{
			name: "400_bad_json",
			setup: func(t *testing.T) (*handler, string) {
				h, _, _ := newTriggerHandlerWithStore(t, &fakeDispatcher{})
				return h, ""
			},
			body:   `{not json`,
			status: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := tc.setup(t)
			req := httptest.NewRequest(http.MethodPost, "/api/daemon/trigger", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			h.handleDaemonTrigger(rec, req)
			assert.Equal(t, tc.status, rec.Code)

			// Deprecation + Sunset headers are always present — even on
			// error paths — so clients can pick up the signal without a
			// 2xx.
			assert.Equal(t, "true", rec.Header().Get("Deprecation"),
				"Deprecation header missing on %d response", rec.Code)
			assert.Equal(t, triggerSunset, rec.Header().Get("Sunset"))
			link := rec.Header().Get("Link")
			assert.Contains(t, link, "/api/v1/scans/ad-hoc")
			assert.Contains(t, link, "successor-version")
		})
	}
}
