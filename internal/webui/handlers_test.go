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
		TargetState: model.TargetState{
			AssetsByType:      map[model.AssetType]int{model.AssetTypeSubdomain: 3},
			AssetsTotal:       3,
			FindingsOpen:      map[model.Severity]int{model.SeverityCritical: 1, model.SeverityHigh: 1},
			FindingsOpenTotal: 2,
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

	// agent-spec 2.0: last_scan must surface the three aggregates so the
	// dashboard can render them without a second round-trip.
	assert.Equal(t, 3, resp.LastScan.TargetState.AssetsByType[model.AssetTypeSubdomain],
		"last_scan.target_state must surface the persisted snapshot")
	assert.Equal(t, 2, resp.LastScan.TargetState.FindingsOpenTotal)
}

// TestChangeSummaryFromDelta is the regression gate for the latent webui
// bug where getChangeSummary always returned new_findings == 0 because it
// recomputed from asset_changes (which only tracks asset deltas, not
// findings). After the SUR-244 cleanup the dashboard reads the persisted
// ScanDelta directly, so new_findings / resolved_findings are now correct.
func TestChangeSummaryFromDelta(t *testing.T) {
	delta := model.ScanDelta{
		NewAssets:         map[model.AssetType]int{model.AssetTypeSubdomain: 2, model.AssetTypeURL: 1},
		DisappearedAssets: map[model.AssetType]int{model.AssetTypeIPv4: 1},
		ModifiedAssets:    map[model.AssetType]int{model.AssetTypePort: 1},
		NewFindings:       map[model.Severity]int{model.SeverityCritical: 1, model.SeverityHigh: 2},
		ResolvedFindings:  map[model.Severity]int{model.SeverityMedium: 1},
	}

	cs := changeSummaryFromDelta(delta)
	assert.Equal(t, 3, cs.NewAssets, "totals across asset types")
	assert.Equal(t, 1, cs.DisappearedAssets)
	assert.Equal(t, 1, cs.ModifiedAssets)
	assert.Equal(t, 3, cs.NewFindings, "must include findings, not just asset_changes (was always 0 pre-SUR-244)")
	assert.Equal(t, 1, cs.ResolvedFindings)
	assert.False(t, cs.IsBaseline)

	baseline := changeSummaryFromDelta(model.ScanDelta{IsBaseline: true})
	assert.True(t, baseline.IsBaseline, "baseline flag must propagate")
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

func TestHandleFindingsScanIDFilter(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	// Seed a second scan with its own finding so we can prove the
	// filter scopes results to the requested scan_id only.
	ctx := context.Background()
	target, err := s.GetTargetByValue(ctx, "example.com")
	require.NoError(t, err)

	now := time.Now().UTC()
	started := now.Add(-2 * time.Minute)
	scan2 := &model.Scan{
		TargetID:   target.ID,
		Type:       model.ScanTypeQuick,
		Status:     model.ScanStatusCompleted,
		Phase:      "completed",
		Progress:   100,
		StartedAt:  &started,
		FinishedAt: &now,
	}
	require.NoError(t, s.CreateScan(ctx, scan2))

	assets, err := s.ListAssets(ctx, storage.AssetListOptions{TargetID: target.ID, Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, assets)

	scan2Finding := &model.Finding{
		AssetID:      assets[0].ID,
		ScanID:       scan2.ID,
		TemplateID:   "exposed-env-file",
		TemplateName: "Exposed .env",
		Severity:     model.SeverityHigh,
		Title:        "Exposed environment file",
		Description:  "found .env at web root",
		Status:       model.FindingStatusOpen,
		SourceTool:   "nuclei",
		Confidence:   90,
	}
	require.NoError(t, s.UpsertFinding(ctx, scan2Finding))

	// Sanity: without scan_id we see all 3 findings.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var all findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &all))
	assert.Equal(t, 3, all.Total)

	// Filter by scan2.ID — must return only the single finding tied to it.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/findings?scan_id="+scan2.ID, nil)
	w2 := httptest.NewRecorder()
	h.handleFindings(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	var filtered findingsResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &filtered))
	assert.Equal(t, 1, filtered.Total)
	require.Len(t, filtered.Findings, 1)
	assert.Equal(t, scan2.ID, filtered.Findings[0].ScanID)
	assert.Equal(t, "exposed-env-file", filtered.Findings[0].TemplateID)
}

func TestHandleFindingsScanIDInvalid(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?scan_id=not-a-uuid", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "scan_id")
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

// --- Grouped Findings Tests ---

// seedGroupedData creates 3 port_service assets for the same host and
// 2 templates per asset, resulting in 6 raw findings but 2 grouped rows.
func seedGroupedData(t *testing.T, s *storage.SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	target := &model.Target{Value: "grouped.example.com"}
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
	}
	require.NoError(t, s.CreateScan(ctx, scan))

	ports := []string{"grouped.example.com:80/tcp", "grouped.example.com:443/tcp", "grouped.example.com:8080/tcp"}
	templates := []struct {
		id       string
		name     string
		title    string
		severity model.Severity
	}{
		{"apache-detect", "Apache Detection", "Apache HTTP Server Detected", model.SeverityInfo},
		{"http-missing-headers", "Missing Headers", "Missing Security Headers", model.SeverityMedium},
	}

	for _, port := range ports {
		asset := &model.Asset{
			TargetID: target.ID,
			Type:     model.AssetTypePort,
			Value:    port,
			Status:   model.AssetStatusActive,
		}
		require.NoError(t, s.UpsertAsset(ctx, asset))

		for _, tmpl := range templates {
			f := &model.Finding{
				AssetID:      asset.ID,
				ScanID:       scan.ID,
				TemplateID:   tmpl.id,
				TemplateName: tmpl.name,
				Severity:     tmpl.severity,
				Title:        tmpl.title,
				Status:       model.FindingStatusOpen,
				SourceTool:   "nuclei",
				Confidence:   80,
			}
			require.NoError(t, s.UpsertFinding(ctx, f))
		}
	}
}

func TestHandleFindingsGrouped(t *testing.T) {
	h, s := newTestHandler(t)
	seedGroupedData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings/grouped", nil)
	w := httptest.NewRecorder()
	h.handleFindingsGrouped(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp groupedFindingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// 2 templates x 1 host = 2 grouped rows
	assert.Equal(t, 2, resp.Total)
	require.Len(t, resp.Groups, 2)

	for _, g := range resp.Groups {
		assert.Equal(t, "grouped.example.com", g.Host)
		assert.Equal(t, "nuclei", g.SourceTool)
		// Each template matched all 3 port assets
		assert.Equal(t, 3, g.AffectedAssetsCount)
		assert.Len(t, g.FindingIDs, 3)
	}
}

func TestHandleFindingsGroupedSeverityFilter(t *testing.T) {
	h, s := newTestHandler(t)
	seedGroupedData(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings/grouped?severity=info", nil)
	w := httptest.NewRecorder()
	h.handleFindingsGrouped(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp groupedFindingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
	require.Len(t, resp.Groups, 1)
	assert.Equal(t, "apache-detect", resp.Groups[0].TemplateID)
}

func TestHandleFindingsRawUnchanged(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	// Raw endpoint unchanged — same response shape.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, len(resp.Findings))
	assert.Equal(t, 2, resp.Total)
	// Verify response has the expected fields (byte-compatible)
	assert.NotEmpty(t, resp.Findings[0].ID)
	assert.NotEmpty(t, resp.Findings[0].AssetID)
}

func TestHandleFindingsRawScanIDFilterStillWorks(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s)

	// Get the scan ID from seed data
	scans, err := s.ListScans(context.Background(), "", 1)
	require.NoError(t, err)
	require.NotEmpty(t, scans)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?scan_id="+scans[0].ID, nil)
	w := httptest.NewRecorder()
	h.handleFindings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp findingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Total)
	for _, f := range resp.Findings {
		assert.Equal(t, scans[0].ID, f.ScanID)
	}
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

// --- Cancel Scan Tests ---

func TestHandleCancelScanNotRunning(t *testing.T) {
	h, s := newTestHandler(t)
	seedTestData(t, s) // seeds a completed scan

	scans, _ := s.ListScans(context.Background(), "", 1)
	require.NotEmpty(t, scans)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scans/"+scans[0].ID, nil)
	w := httptest.NewRecorder()
	h.handleCancelScan(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "scan is not running", resp["error"])
}

func TestHandleCancelScanNotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scans/nonexistent-id", nil)
	w := httptest.NewRecorder()
	h.handleCancelScan(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleCancelScanRunning(t *testing.T) {
	h, s := newTestHandler(t)
	ctx := context.Background()

	target := &model.Target{Value: "cancel-test.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	now := time.Now().UTC()
	scan := &model.Scan{
		TargetID:  target.ID,
		Type:      model.ScanTypeFull,
		Status:    model.ScanStatusRunning,
		Phase:     "discovery",
		Progress:  25,
		StartedAt: &now,
	}
	require.NoError(t, s.CreateScan(ctx, scan))

	// Simulate a running scan with a cancel func
	cancelled := false
	h.scanMu.Lock()
	h.runningScan = scan.ID
	h.cancelFunc = func() { cancelled = true }
	h.scanMu.Unlock()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scans/"+scan.ID, nil)
	w := httptest.NewRecorder()
	h.handleCancelScan(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]bool
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"])
	assert.True(t, cancelled, "cancel function should have been called")
}
