package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func newTestHandler(t *testing.T) (*handler, *storage.SQLiteStore) {
	t.Helper()
	s := newTestStore(t)
	return &handler{store: s, version: "test"}, s
}

func seedTestData(t *testing.T, s *storage.SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	now := time.Now().UTC()
	started := now.Add(-5 * time.Minute)
	scan := &model.Scan{
		TargetID:   target.ID,
		Type:       model.ScanTypeFull,
		Status:     model.ScanStatusCompleted,
		Phase:      "assessment",
		Progress:   100,
		StartedAt:  &started,
		FinishedAt: &now,
		Stats: model.ScanStats{
			SubdomainsFound:  3,
			FindingsTotal:    2,
			FindingsCritical: 1,
			FindingsHigh:     1,
		},
	}
	require.NoError(t, s.CreateScan(ctx, scan))

	asset := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "blog.example.com",
		Status:   model.AssetStatusActive,
	}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	finding := &model.Finding{
		AssetID:      asset.ID,
		ScanID:       scan.ID,
		TemplateID:   "wordpress-xmlrpc",
		TemplateName: "WordPress XML-RPC",
		Severity:     model.SeverityCritical,
		Title:        "WordPress XML-RPC Enabled",
		Description:  "XML-RPC is enabled",
		Status:       model.FindingStatusOpen,
		SourceTool:   "nuclei",
		Confidence:   95,
	}
	require.NoError(t, s.UpsertFinding(ctx, finding))

	finding2 := &model.Finding{
		AssetID:      asset.ID,
		ScanID:       scan.ID,
		TemplateID:   "http-missing-headers",
		TemplateName: "Missing Headers",
		Severity:     model.SeverityHigh,
		Title:        "Missing Security Headers",
		Description:  "Headers missing",
		Status:       model.FindingStatusOpen,
		SourceTool:   "nuclei",
		Confidence:   80,
	}
	require.NoError(t, s.UpsertFinding(ctx, finding2))

	toolRun := &model.ToolRun{
		ScanID:        scan.ID,
		ToolName:      "nuclei",
		Phase:         "assessment",
		Status:        model.ToolRunCompleted,
		StartedAt:     started,
		DurationMs:    12000,
		FindingsCount: 2,
		TargetsCount:  1,
	}
	require.NoError(t, s.CreateToolRun(ctx, toolRun))
}

func TestHandleOverview(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	w := httptest.NewRecorder()
	h.handleOverview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp overviewResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, 2, resp.TotalFindings)
	assert.Equal(t, 1, resp.TotalAssets)
	assert.Equal(t, 1, resp.FindingsBySeverity[model.SeverityCritical])
	assert.Equal(t, 1, resp.FindingsBySeverity[model.SeverityHigh])
	assert.NotNil(t, resp.LastScan)
	assert.Equal(t, "example.com", resp.LastScan.Target)
	// Score = 100 - 25*1 - 10*1 = 65
	assert.Equal(t, 65, resp.SecurityScore)
	assert.Equal(t, "test", resp.Agent.Version)
	// Score breakdown
	require.Len(t, resp.ScoreBreakdown, 2)
	assert.Equal(t, "critical", resp.ScoreBreakdown[0].Severity)
	assert.Equal(t, 25, resp.ScoreBreakdown[0].Penalty)
}

func TestHandleFindings(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, len(resp.Findings))
	assert.Equal(t, 2, resp.Total)
	assert.Equal(t, model.SeverityCritical, resp.Findings[0].Severity)
}

func TestHandleFindingsSeverityFilter(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?severity=critical", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, len(resp.Findings))
	// Total should reflect filtered count, not global count
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, model.SeverityCritical, resp.Findings[0].Severity)
}

func TestHandleFindingsPagination(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	// Page 1 with limit 1
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?limit=1&page=1", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, len(resp.Findings))
	assert.Equal(t, 2, resp.Total)
	assert.Equal(t, 1, resp.Page)

	// Page 2
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/findings?limit=1&page=2", nil)
	w2 := httptest.NewRecorder()
	h.handleFindings(w2, req2)

	var resp2 findingsResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, 1, len(resp2.Findings))
	assert.Equal(t, 2, resp2.Page)
	// Different finding than page 1
	assert.NotEqual(t, resp.Findings[0].ID, resp2.Findings[0].ID)
}

func TestHandleFindingDetail(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	findings, _ := s.ListFindings(context.Background(), storage.FindingListOptions{Limit: 1})
	require.NotEmpty(t, findings)
	id := findings[0].ID

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings/"+id, nil)
	w := httptest.NewRecorder()
	h.handleFindingDetail(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "finding")
	assert.Contains(t, resp, "asset")
}

func TestHandleFindingDetailNotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings/nonexistent", nil)
	w := httptest.NewRecorder()
	h.handleFindingDetail(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleAssets(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil)
	w := httptest.NewRecorder()
	h.handleAssets(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp assetsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, len(resp.Assets))
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, "blog.example.com", resp.Assets[0].Value)
}

func TestHandleAssetTree(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/tree", nil)
	w := httptest.NewRecorder()
	h.handleAssetTree(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string][]assetTreeNode
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp["tree"])
	// Tree nodes should have finding counts
	if len(resp["tree"]) > 0 {
		assert.Equal(t, 2, resp["tree"][0].FindingCount)
	}
}

func TestHandleScans(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans", nil)
	w := httptest.NewRecorder()
	h.handleScans(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp scansResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, len(resp.Scans))
	assert.Equal(t, model.ScanStatusCompleted, resp.Scans[0].Status)
	// Should include resolved target name
	assert.Equal(t, "example.com", resp.Scans[0].Target)
}

func TestHandleScanDetail(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	scans, _ := s.ListScans(context.Background(), "", 1)
	require.NotEmpty(t, scans)
	id := scans[0].ID

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+id, nil)
	w := httptest.NewRecorder()
	h.handleScanDetail(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "scan")
	assert.Contains(t, resp, "tool_runs")
	assert.Contains(t, resp, "target")
}

func TestHandleTargets(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	w := httptest.NewRecorder()
	h.handleTargets(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string][]targetWithStats
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	targets := resp["targets"]
	require.Equal(t, 1, len(targets))
	assert.Equal(t, "example.com", targets[0].Value)
	assert.Equal(t, 2, targets[0].FindingCount)
	assert.Equal(t, 1, targets[0].AssetCount)
	assert.Equal(t, 1, targets[0].ScanCount)
}

func TestHandleTools(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	w := httptest.NewRecorder()
	h.handleTools(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string][]model.ToolRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	tools := resp["tools"]
	require.Equal(t, 1, len(tools))
	assert.Equal(t, "nuclei", tools[0].ToolName)
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)

	endpoints := []struct {
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"/api/v1/overview", h.handleOverview},
		{"/api/v1/findings", h.handleFindings},
		{"/api/v1/assets", h.handleAssets},
		{"/api/v1/scans", h.handleScans},
		{"/api/v1/targets", h.handleTargets},
		{"/api/v1/tools", h.handleTools},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, ep.path, nil)
		w := httptest.NewRecorder()
		ep.handler(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "POST to %s should be 405", ep.path)
	}
}

func TestEmptyDatabase(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	w := httptest.NewRecorder()
	h.handleOverview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp overviewResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 100, resp.SecurityScore)
	assert.Equal(t, 0, resp.TotalFindings)
	assert.Equal(t, 0, resp.TotalAssets)
	assert.Nil(t, resp.LastScan)
	assert.Empty(t, resp.ScoreBreakdown)
}

// --- Target CRUD Tests ---

func TestHandleCreateTarget(t *testing.T) {
	h, _ := newTestHandler(t)

	body := `{"value":"newdomain.com","scope":"external"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCreateTarget(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "target")
}

func TestHandleCreateTargetDuplicate(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s) // seeds example.com

	body := `{"value":"example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCreateTarget(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandleCreateTargetEmptyValue(t *testing.T) {
	h, _ := newTestHandler(t)

	body := `{"value":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCreateTarget(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleDeleteTarget(t *testing.T) {
	h, s := newTestHandler(t)

	ctx := context.Background()
	target := &model.Target{Value: "todelete.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/targets/"+target.ID, nil)
	w := httptest.NewRecorder()
	h.handleDeleteTarget(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify deleted
	_, err := s.GetTarget(ctx, target.ID)
	assert.Error(t, err)
}

func TestHandleDeleteTargetNotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/targets/nonexistent", nil)
	w := httptest.NewRecorder()
	h.handleDeleteTarget(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Finding Status Update Tests ---

func TestHandleUpdateFindingStatus(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	findings, _ := s.ListFindings(context.Background(), storage.FindingListOptions{Limit: 1})
	require.NotEmpty(t, findings)
	id := findings[0].ID

	body := `{"status":"acknowledged"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/findings/"+id+"/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleUpdateFindingStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify status changed
	updated, err := s.GetFinding(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.FindingStatusAcknowledged, updated.Status)
}

func TestHandleUpdateFindingStatusResolve(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	findings, _ := s.ListFindings(context.Background(), storage.FindingListOptions{Limit: 1})
	require.NotEmpty(t, findings)
	id := findings[0].ID

	body := `{"status":"resolved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/findings/"+id+"/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleUpdateFindingStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	updated, err := s.GetFinding(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.FindingStatusResolved, updated.Status)
	assert.NotNil(t, updated.ResolvedAt)
}

func TestHandleUpdateFindingStatusInvalid(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	findings, _ := s.ListFindings(context.Background(), storage.FindingListOptions{Limit: 1})
	require.NotEmpty(t, findings)
	id := findings[0].ID

	body := `{"status":"invalid_status"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/findings/"+id+"/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleUpdateFindingStatus(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUpdateFindingStatusNotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	body := `{"status":"resolved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/findings/nonexistent/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleUpdateFindingStatus(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Scan Trigger Tests ---

func TestHandleCreateScanNoRegistry(t *testing.T) {
	h, _ := newTestHandler(t) // handler has nil registry

	body := `{"target_id":"some-id"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCreateScan(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleCreateScanMissingTarget(t *testing.T) {
	h, _ := newTestHandler(t)
	h.registry = &detection.Registry{}

	body := `{"target_id":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCreateScan(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleScanStatus(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scans/status", nil)
	w := httptest.NewRecorder()
	h.handleScanStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["scanning"])
}

func TestHandleAvailableTools(t *testing.T) {
	h, _ := newTestHandler(t)
	// nil registry returns empty list
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools/available", nil)
	w := httptest.NewRecorder()
	h.handleAvailableTools(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	tools := resp["tools"].([]interface{})
	assert.Equal(t, 0, len(tools))
}
