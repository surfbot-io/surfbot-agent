package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// seedScanLogs creates a scan + tool_run and inserts n log lines.
// Returns the scan ID. Used by every scan_logs handler test.
func seedScanLogs(t *testing.T, s *storage.SQLiteStore, n int) string {
	t.Helper()
	ctx := context.Background()
	target := &model.Target{Value: "logs.example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))
	now := time.Now().UTC()
	scan := &model.Scan{
		TargetID:  target.ID,
		Type:      model.ScanTypeFull,
		Status:    model.ScanStatusRunning,
		Phase:     "discovery",
		StartedAt: &now,
	}
	require.NoError(t, s.CreateScan(ctx, scan))
	logs := make([]model.ScanLog, 0, n)
	for i := 0; i < n; i++ {
		level := model.LogLevelInfo
		if i%5 == 0 {
			level = model.LogLevelWarn
		}
		source := "scanner"
		if i%2 == 0 {
			source = "subfinder"
		}
		logs = append(logs, model.ScanLog{
			ScanID:    scan.ID,
			Source:    source,
			Level:     level,
			Text:      "line",
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	if len(logs) > 0 {
		require.NoError(t, s.InsertScanLogs(ctx, logs))
	}
	return scan.ID
}

func TestHandleScanLogs_HEAD_OK(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 0)

	req := httptest.NewRequest(http.MethodHead, "/api/v1/scans/"+scanID+"/logs", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.Bytes(), "HEAD response body should be empty")
}

func TestHandleScanLogs_HEAD_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodHead, "/api/v1/scans/00000000-0000-0000-0000-000000000000/logs", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleScanLogs_GET_BasicShape(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 5)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "lines")
	assert.Contains(t, resp, "next")
	assert.Contains(t, resp, "has_more")
	lines, _ := resp["lines"].([]any)
	require.Len(t, lines, 5)
	first, _ := lines[0].(map[string]any)
	assert.NotEmpty(t, first["scan_id"])
	assert.NotEmpty(t, first["text"])
	assert.NotEmpty(t, first["level"])
	assert.NotEmpty(t, first["source"])
}

func TestHandleScanLogs_GET_PaginationCursor(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 50)

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs?limit=10", nil)
	w1 := httptest.NewRecorder()
	h.handleScanLogs(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)
	var page1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &page1))
	assert.True(t, page1["has_more"].(bool))
	cursor := int64(page1["next"].(float64))

	url2 := "/api/v1/scans/" + scanID + "/logs?limit=10&since=" + strconv.FormatInt(cursor, 10)
	req2 := httptest.NewRequest(http.MethodGet, url2, nil)
	w2 := httptest.NewRecorder()
	h.handleScanLogs(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	var page2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))
	lines2, _ := page2["lines"].([]any)
	require.Len(t, lines2, 10)
	first2 := lines2[0].(map[string]any)
	assert.Greater(t, int64(first2["id"].(float64)), cursor)
}

func TestHandleScanLogs_GET_LimitCap(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs?limit=5000", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleScanLogs_GET_LimitInvalid(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs?limit=-5", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleScanLogs_GET_LevelFilter(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 25) // every 5th line is warn → 5 warns

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs?level=warn", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	lines := resp["lines"].([]any)
	assert.Equal(t, 5, len(lines), "5 of 25 lines should be warn")
}

func TestHandleScanLogs_GET_SourceFilter(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 10) // 5 subfinder + 5 scanner

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+scanID+"/logs?source=subfinder", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	lines := resp["lines"].([]any)
	require.Equal(t, 5, len(lines))
	for _, ln := range lines {
		assert.Equal(t, "subfinder", ln.(map[string]any)["source"])
	}
}

func TestHandleScanLogs_GET_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/00000000-0000-0000-0000-000000000000/logs", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleScanLogs_MethodNotAllowed(t *testing.T) {
	h, s := newTestHandler(t)
	scanID := seedScanLogs(t, s, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scans/"+scanID+"/logs", nil)
	w := httptest.NewRecorder()
	h.handleScanLogs(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
