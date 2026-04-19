package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyScheduleAPI_Returns410(t *testing.T) {
	h := &handler{}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/schedule", strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			h.handleSchedule(rec, req)
			require.Equal(t, http.StatusGone, rec.Code, "want 410 for %s", method)

			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, "deprecated", body["error"])
			assert.Equal(t, "/api/v1/schedules", body["migrated_to"])
			assert.Equal(t, "3.0", body["agent_spec_version_required"])
			assert.NotEmpty(t, body["message"])
		})
	}
}

func TestLegacyScheduleAPI_Headers(t *testing.T) {
	h := &handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/schedule", nil)
	rec := httptest.NewRecorder()
	h.handleSchedule(rec, req)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "true", rec.Header().Get("Deprecation"))
	assert.Contains(t, rec.Header().Get("Link"), "/api/v1/schedules")
}
