package webui

// PR8 (#41) — /assets accepts a `status` query param so the UI v2 Changes
// page can fetch the new/disappeared/returned columns from a single
// endpoint without an extra time-windowed diff handler. Validation
// matches /findings: an unknown status passes straight through to the
// store, which yields zero rows. Test covers the happy path (status
// filters down) plus regression on the no-filter case.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestAssetsListAcceptsStatusParam(t *testing.T) {
	h, s := newTestHandler(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	mk := func(value string, status model.AssetStatus) {
		a := &model.Asset{
			TargetID: target.ID,
			Type:     model.AssetTypeSubdomain,
			Value:    value,
			Status:   status,
		}
		require.NoError(t, s.UpsertAsset(ctx, a))
	}
	mk("api.example.com", model.AssetStatusNew)
	mk("staging.example.com", model.AssetStatusNew)
	mk("old.example.com", model.AssetStatusDisappeared)
	mk("blog.example.com", model.AssetStatusActive)

	cases := []struct {
		status string
		want   int
	}{
		{"new", 2},
		{"disappeared", 1},
		{"active", 1},
		{"returned", 0},
		// Unknown status: handler does not 400; store returns zero rows.
		{"banana", 0},
	}
	for _, c := range cases {
		c := c
		t.Run(c.status, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/assets?status="+c.status, nil)
			w := httptest.NewRecorder()
			h.handleAssets(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			var resp assetsResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, c.want, len(resp.Assets), "status=%s should return %d assets", c.status, c.want)
			for _, a := range resp.Assets {
				if c.status != "banana" {
					assert.Equal(t, model.AssetStatus(c.status), a.Status)
				}
			}
		})
	}

	// Regression: no status filter still returns every asset.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil)
	w := httptest.NewRecorder()
	h.handleAssets(w, req)
	var resp assetsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 4, len(resp.Assets), "/assets without status must return all rows")
}
