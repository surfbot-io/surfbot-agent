package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
	"github.com/surfbot-io/surfbot-agent/internal/scoring"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

type handler struct {
	store    *storage.SQLiteStore
	version  string
	registry *detection.Registry
	daemon   *DaemonView // optional: SPEC-X3.1 agent card data source

	// scanMu protects runningScan to prevent concurrent scans.
	scanMu      sync.Mutex
	runningScan string // scan ID of the currently running scan, empty if idle
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}
	return json.Unmarshal(body, v)
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// --- Overview ---

type overviewResponse struct {
	SecurityScore      int                     `json:"security_score"`
	ScoreBreakdown     []scoring.Component     `json:"score_breakdown"`
	TotalFindings      int                     `json:"total_findings"`
	UniqueFindings     int                     `json:"unique_findings"`
	FindingsBySeverity map[model.Severity]int  `json:"findings_by_severity"`
	TotalAssets        int                     `json:"total_assets"`
	AssetsByType       map[model.AssetType]int `json:"assets_by_type"`
	LastScan           *scanSummary            `json:"last_scan"`
	ChangesSinceLast   *changeSummary          `json:"changes_since_last"`
	Agent              agentInfo               `json:"agent"`
}


type scanSummary struct {
	ID              string     `json:"id"`
	Target          string     `json:"target"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	DurationSeconds int        `json:"duration_seconds"`
	FindingsCount   int        `json:"findings_count"`
}

type changeSummary struct {
	NewFindings       int `json:"new_findings"`
	NewAssets         int `json:"new_assets"`
	DisappearedAssets int `json:"disappeared_assets"`
}

type agentInfo struct {
	Version      string `json:"version"`
	DBPath       string `json:"db_path"`
	DBSizeBytes  int64  `json:"db_size_bytes"`
	TargetsCount int    `json:"targets_count"`
}

func (h *handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	var errs []error

	totalFindings, err := h.store.CountFindings(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count findings: %w", err))
	}
	totalAssets, err := h.store.CountAssets(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count assets: %w", err))
	}
	targetsCount, err := h.store.CountTargets(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count targets: %w", err))
	}
	sevCounts, err := h.store.CountFindingsBySeverity(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count severity: %w", err))
	}
	typeCounts, err := h.store.CountAssetsByType(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count types: %w", err))
	}
	uniqueFindings, err := h.store.CountUniqueFindingsByHost(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("count unique findings: %w", err))
	}

	if len(errs) > 0 {
		for _, e := range errs {
			log.Printf("[webui] overview error: %v", e)
		}
	}

	if sevCounts == nil {
		sevCounts = make(map[model.Severity]int)
	}
	if typeCounts == nil {
		typeCounts = make(map[model.AssetType]int)
	}

	score, breakdown := scoring.ComputeSecurityScore(sevCounts)

	resp := overviewResponse{
		SecurityScore:      score,
		ScoreBreakdown:     breakdown,
		TotalFindings:      totalFindings,
		UniqueFindings:     uniqueFindings,
		FindingsBySeverity: sevCounts,
		TotalAssets:        totalAssets,
		AssetsByType:       typeCounts,
		Agent: agentInfo{
			Version:      h.version,
			DBPath:       h.store.DBPath(),
			TargetsCount: targetsCount,
		},
	}

	if info, err := os.Stat(h.store.DBPath()); err == nil {
		resp.Agent.DBSizeBytes = info.Size()
	}

	if last, err := h.store.LastScan(ctx); err == nil && last != nil {
		target, _ := h.store.GetTarget(ctx, last.TargetID)
		targetName := last.TargetID
		if target != nil {
			targetName = target.Value
		}
		var dur int
		if last.StartedAt != nil && last.FinishedAt != nil {
			dur = int(last.FinishedAt.Sub(*last.StartedAt).Seconds())
		}
		resp.LastScan = &scanSummary{
			ID:              last.ID,
			Target:          targetName,
			Status:          string(last.Status),
			StartedAt:       last.StartedAt,
			FinishedAt:      last.FinishedAt,
			DurationSeconds: dur,
			FindingsCount:   last.Stats.FindingsTotal,
		}
		resp.ChangesSinceLast = h.getChangeSummary(ctx, last.ID)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) getChangeSummary(ctx context.Context, scanID string) *changeSummary {
	changes, err := h.store.ListAssetChanges(ctx, storage.AssetChangeListOptions{
		ScanID: scanID,
		Limit:  1000,
	})
	if err != nil {
		return nil
	}

	cs := &changeSummary{}
	for _, c := range changes {
		switch c.ChangeType {
		case model.ChangeTypeAppeared:
			cs.NewAssets++
		case model.ChangeTypeDisappeared:
			cs.DisappearedAssets++
		}
	}
	return cs
}

// --- Findings ---

type findingsResponse struct {
	Findings []model.Finding `json:"findings"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	Limit    int             `json:"limit"`
}

func (h *handler) handleFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 50)
	if limit > 250 {
		limit = 250
	}

	opts := storage.FindingListOptions{
		Limit:  limit,
		Offset: (page - 1) * limit,
	}
	if sev := r.URL.Query().Get("severity"); sev != "" {
		opts.Severity = model.Severity(sev)
	}
	if tool := r.URL.Query().Get("tool"); tool != "" {
		opts.SourceTool = tool
	}
	if status := r.URL.Query().Get("status"); status != "" {
		opts.Status = model.FindingStatus(status)
	}
	if targetID := r.URL.Query().Get("target_id"); targetID != "" {
		opts.TargetID = targetID
	}
	if scanID := r.URL.Query().Get("scan_id"); scanID != "" {
		if _, err := uuid.Parse(scanID); err != nil {
			writeError(w, http.StatusBadRequest, "scan_id must be a valid UUID")
			return
		}
		opts.ScanID = scanID
	}
	if tmpl := r.URL.Query().Get("template_id"); tmpl != "" {
		opts.TemplateID = tmpl
	}
	if host := r.URL.Query().Get("host"); host != "" {
		opts.Host = host
	}

	findings, err := h.store.ListFindings(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list findings")
		return
	}

	total, _ := h.store.CountFindingsFiltered(r.Context(), opts)

	writeJSON(w, http.StatusOK, findingsResponse{
		Findings: findings,
		Total:    total,
		Page:     page,
		Limit:    limit,
	})
}

func (h *handler) handleFindingDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/findings/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing finding id")
		return
	}

	finding, err := h.store.GetFinding(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "finding not found")
		return
	}

	asset, _ := h.store.GetAsset(r.Context(), finding.AssetID)

	writeJSON(w, http.StatusOK, map[string]any{
		"finding": finding,
		"asset":   asset,
	})
}

// --- Grouped Findings ---

type groupedFindingsResponse struct {
	Groups []storage.GroupedFinding `json:"groups"`
	Total  int                     `json:"total"`
	Page   int                     `json:"page"`
	Limit  int                     `json:"limit"`
}

func (h *handler) handleFindingsGrouped(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 50)
	if limit > 250 {
		limit = 250
	}

	opts := storage.GroupedFindingOptions{
		Limit:  limit,
		Offset: (page - 1) * limit,
		SortBy: r.URL.Query().Get("sort"),
	}
	if sev := r.URL.Query().Get("severity"); sev != "" {
		opts.Severity = model.Severity(sev)
	}
	if tool := r.URL.Query().Get("tool"); tool != "" {
		opts.SourceTool = tool
	}
	if host := r.URL.Query().Get("host"); host != "" {
		opts.Host = host
	}

	groups, err := h.store.ListGroupedFindings(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list grouped findings")
		return
	}

	total, _ := h.store.CountGroupedFindings(r.Context(), opts)

	writeJSON(w, http.StatusOK, groupedFindingsResponse{
		Groups: groups,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// --- Assets ---

type assetsResponse struct {
	Assets []model.Asset `json:"assets"`
	Total  int           `json:"total"`
	Page   int           `json:"page"`
	Limit  int           `json:"limit"`
}

func (h *handler) handleAssets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 100)
	if limit > 250 {
		limit = 250
	}

	opts := storage.AssetListOptions{
		Limit:  limit,
		Offset: (page - 1) * limit,
	}
	if typ := r.URL.Query().Get("type"); typ != "" {
		opts.Type = model.AssetType(typ)
	}
	if targetID := r.URL.Query().Get("target_id"); targetID != "" {
		opts.TargetID = targetID
	}

	assets, err := h.store.ListAssets(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list assets")
		return
	}

	total, _ := h.store.CountAssetsFiltered(r.Context(), opts)

	writeJSON(w, http.StatusOK, assetsResponse{
		Assets: assets,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// assetTreeNode represents a node in the hierarchical asset tree.
type assetTreeNode struct {
	model.Asset
	Children     []assetTreeNode `json:"children"`
	FindingCount int             `json:"finding_count"`
}

func (h *handler) handleAssetTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	targetID := r.URL.Query().Get("target_id")

	assets, err := h.store.ListAssets(r.Context(), storage.AssetListOptions{
		TargetID: targetID,
		Limit:    10000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list assets")
		return
	}

	// Single GROUP BY query instead of loading all findings
	findingCounts, err := h.store.CountFindingsByAssetIDs(r.Context())
	if err != nil {
		log.Printf("[webui] asset tree finding counts error: %v", err)
		findingCounts = make(map[string]int)
	}

	// Build tree
	nodeMap := make(map[string]*assetTreeNode)
	var roots []assetTreeNode

	for _, a := range assets {
		node := assetTreeNode{
			Asset:        a,
			Children:     make([]assetTreeNode, 0),
			FindingCount: findingCounts[a.ID],
		}
		nodeMap[a.ID] = &node
	}

	for _, a := range assets {
		node := nodeMap[a.ID]
		if a.ParentID != "" {
			if parent, ok := nodeMap[a.ParentID]; ok {
				parent.Children = append(parent.Children, *node)
				continue
			}
		}
		roots = append(roots, *node)
	}

	if roots == nil {
		roots = make([]assetTreeNode, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{"tree": roots})
}

// --- Scans ---

type scanListEntry struct {
	model.Scan
	Target string `json:"target"`
}

type scansResponse struct {
	Scans []scanListEntry `json:"scans"`
	Total int             `json:"total"`
	Limit int             `json:"limit"`
}

func (h *handler) handleScans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := queryInt(r, "limit", 20)
	targetID := r.URL.Query().Get("target_id")

	scans, err := h.store.ListScans(r.Context(), targetID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list scans")
		return
	}

	// Resolve target names (cache to avoid repeated lookups)
	targetNames := make(map[string]string)
	entries := make([]scanListEntry, 0, len(scans))
	for _, s := range scans {
		name, ok := targetNames[s.TargetID]
		if !ok {
			if t, err := h.store.GetTarget(r.Context(), s.TargetID); err == nil && t != nil {
				name = t.Value
			} else {
				name = s.TargetID
			}
			targetNames[s.TargetID] = name
		}
		entries = append(entries, scanListEntry{Scan: s, Target: name})
	}

	total, _ := h.store.CountScans(r.Context())

	writeJSON(w, http.StatusOK, scansResponse{
		Scans: entries,
		Total: total,
		Limit: limit,
	})
}

func (h *handler) handleScanDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/scans/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing scan id")
		return
	}

	scan, err := h.store.GetScan(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}

	toolRuns, _ := h.store.ListToolRuns(r.Context(), id)
	if toolRuns == nil {
		toolRuns = make([]model.ToolRun, 0)
	}

	target, _ := h.store.GetTarget(r.Context(), scan.TargetID)
	targetName := scan.TargetID
	if target != nil {
		targetName = target.Value
	}

	changes, _ := h.store.ListAssetChanges(r.Context(), storage.AssetChangeListOptions{
		ScanID: id,
		Limit:  100,
	})
	if changes == nil {
		changes = make([]model.AssetChange, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scan":      scan,
		"target":    targetName,
		"tool_runs": toolRuns,
		"changes":   changes,
	})
}

// --- Targets ---

type targetWithStats struct {
	model.Target
	FindingCount int `json:"finding_count"`
	AssetCount   int `json:"asset_count"`
	ScanCount    int `json:"scan_count"`
}

func (h *handler) handleTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	targets, err := h.store.ListTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list targets")
		return
	}

	result := make([]targetWithStats, 0, len(targets))
	for _, t := range targets {
		findingCount, _ := h.store.CountFindingsByTargetID(r.Context(), t.ID)
		assetCount, _ := h.store.CountAssetsByTargetID(r.Context(), t.ID)
		scanCount, _ := h.store.CountScansByTargetID(r.Context(), t.ID)

		result = append(result, targetWithStats{
			Target:       t,
			FindingCount: findingCount,
			AssetCount:   assetCount,
			ScanCount:    scanCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"targets": result})
}

// --- Tools ---

func (h *handler) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	lastScan, err := h.store.LastScan(r.Context())
	if err != nil || lastScan == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tools": make([]model.ToolRun, 0)})
		return
	}

	toolRuns, err := h.store.ListToolRuns(r.Context(), lastScan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tool runs")
		return
	}
	if toolRuns == nil {
		toolRuns = make([]model.ToolRun, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{"tools": toolRuns})
}

// --- Target CRUD ---

type createTargetRequest struct {
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
	Scope string `json:"scope,omitempty"`
}

func (h *handler) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req createTargetRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req.Value = strings.TrimSpace(req.Value)
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "target value is required")
		return
	}

	scope := model.TargetScope(req.Scope)
	if scope == "" {
		scope = model.TargetScopeExternal
	}

	t := &model.Target{
		Value: req.Value,
		Scope: scope,
	}
	if req.Type != "" {
		t.Type = model.TargetType(req.Type)
	}

	if err := h.store.CreateTarget(r.Context(), t); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, "target already exists")
			return
		}
		if strings.Contains(err.Error(), "invalid") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("[webui] create target error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create target")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"target": t})
}

func (h *handler) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/targets/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing target id")
		return
	}

	// Try by ID first, then by value
	t, err := h.store.GetTarget(r.Context(), id)
	if err != nil {
		t, err = h.store.GetTargetByValue(r.Context(), id)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}

	if err := h.store.DeleteTarget(r.Context(), t.ID); err != nil {
		log.Printf("[webui] delete target error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to delete target")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"deleted": t.ID})
}

// --- Finding Status Update ---

type updateFindingStatusRequest struct {
	Status string `json:"status"`
}

var validFindingStatuses = map[string]bool{
	string(model.FindingStatusOpen):          true,
	string(model.FindingStatusAcknowledged):  true,
	string(model.FindingStatusResolved):      true,
	string(model.FindingStatusFalsePositive): true,
	string(model.FindingStatusIgnored):       true,
}

func (h *handler) handleUpdateFindingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Path: /api/v1/findings/{id}/status
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/findings/")
	path = strings.TrimSuffix(path, "/status")
	id := path
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing finding id")
		return
	}

	var req updateFindingStatusRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if !validFindingStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "invalid status: must be open, acknowledged, resolved, false_positive, or ignored")
		return
	}

	// Verify finding exists
	finding, err := h.store.GetFinding(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "finding not found")
		return
	}

	newStatus := model.FindingStatus(req.Status)
	if err := h.store.UpdateFindingStatus(r.Context(), finding.ID, newStatus); err != nil {
		log.Printf("[webui] update finding status error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update finding status")
		return
	}

	// Set resolved_at when transitioning to resolved
	if newStatus == model.FindingStatusResolved {
		now := time.Now().UTC()
		h.store.UpdateFindingResolvedAt(r.Context(), finding.ID, &now) //nolint:errcheck
	} else if finding.Status == model.FindingStatusResolved && newStatus != model.FindingStatusResolved {
		// Clear resolved_at if un-resolving
		h.store.UpdateFindingResolvedAt(r.Context(), finding.ID, nil) //nolint:errcheck
	}

	finding.Status = newStatus
	writeJSON(w, http.StatusOK, map[string]any{"finding": finding})
}

// --- Scan Trigger ---

type createScanRequest struct {
	TargetID  string   `json:"target_id"`
	Type      string   `json:"type,omitempty"`       // full, quick, discovery
	Tools     []string `json:"tools,omitempty"`      // specific tools to run (empty = all)
	RateLimit int      `json:"rate_limit,omitempty"` // global rate limit in req/s (0 = per-tool defaults)
	Timeout   int      `json:"timeout,omitempty"`    // per-phase timeout in seconds (0 = default)
}

func (h *handler) handleCreateScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "scan engine not available")
		return
	}

	var req createScanRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.TargetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}

	// Verify target exists
	target, err := h.store.GetTarget(r.Context(), req.TargetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}

	scanType := model.ScanTypeFull
	switch req.Type {
	case "quick":
		scanType = model.ScanTypeQuick
	case "discovery":
		scanType = model.ScanTypeDiscovery
	}

	// Validate tools against registry.
	if len(req.Tools) > 0 {
		for _, name := range req.Tools {
			if _, ok := h.registry.GetByName(name); !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown tool: %s", name))
				return
			}
		}
	}

	// Validate rate_limit and timeout.
	if req.RateLimit < 0 {
		writeError(w, http.StatusBadRequest, "rate_limit must be >= 0")
		return
	}
	if req.Timeout < 0 {
		writeError(w, http.StatusBadRequest, "timeout must be >= 0")
		return
	}

	// Prevent concurrent scans
	h.scanMu.Lock()
	if h.runningScan != "" {
		h.scanMu.Unlock()
		writeError(w, http.StatusConflict, "a scan is already running")
		return
	}

	// Create pipeline and run async
	pipe := pipeline.New(h.store, h.registry)
	opts := pipeline.PipelineOptions{
		ScanType:  scanType,
		Tools:     req.Tools,
		RateLimit: req.RateLimit,
		Timeout:   req.Timeout,
	}

	// Create a placeholder scan ID by peeking at the pipeline
	// We start the pipeline in a goroutine and return immediately
	h.runningScan = "starting"
	h.scanMu.Unlock()

	go func() {
		ctx := context.Background()
		result, err := pipe.Run(ctx, target.ID, opts)

		h.scanMu.Lock()
		h.runningScan = ""
		h.scanMu.Unlock()

		if err != nil {
			log.Printf("[webui] scan error for %s: %v", target.Value, err)
			return
		}
		log.Printf("[webui] scan completed for %s: %d findings, %d assets in %s",
			target.Value, result.TotalFindings, result.TotalAssets, result.Duration)
	}()

	resp := map[string]any{
		"status":  "started",
		"target":  target.Value,
		"type":    string(scanType),
		"message": "scan started in background",
	}
	if len(req.Tools) > 0 {
		resp["tools"] = req.Tools
	}
	if req.RateLimit > 0 {
		resp["rate_limit"] = req.RateLimit
	}
	if req.Timeout > 0 {
		resp["timeout"] = req.Timeout
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// --- Scan Status ---

func (h *handler) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	h.scanMu.Lock()
	running := h.runningScan != ""
	h.scanMu.Unlock()

	// Check for running scan in database
	lastScan, _ := h.store.LastScan(r.Context())
	var activeScan *model.Scan
	if lastScan != nil && lastScan.Status == model.ScanStatusRunning {
		activeScan = lastScan
	}

	resp := map[string]any{
		"scanning": running || activeScan != nil,
	}
	if activeScan != nil {
		target, _ := h.store.GetTarget(r.Context(), activeScan.TargetID)
		targetName := activeScan.TargetID
		if target != nil {
			targetName = target.Value
		}
		resp["scan"] = map[string]any{
			"id":       activeScan.ID,
			"target":   targetName,
			"phase":    activeScan.Phase,
			"progress": activeScan.Progress,
			"type":     activeScan.Type,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Available Tools ---

type toolInfo struct {
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Available bool   `json:"available"`
}

func (h *handler) handleAvailableTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tools": make([]toolInfo, 0)})
		return
	}

	var tools []toolInfo
	for _, t := range h.registry.Tools() {
		tools = append(tools, toolInfo{
			Name:      t.Name(),
			Phase:     t.Phase(),
			Available: t.Available(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tools":     tools,
		"total":     len(tools),
		"available": len(h.registry.AvailableTools()),
	})
}
