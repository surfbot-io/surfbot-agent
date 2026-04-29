package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewSQLiteStore(t *testing.T) {
	s := newTestStore(t)

	// Verify tables exist by querying them
	ctx := context.Background()
	tables := []string{"targets", "assets", "scans", "findings", "tool_runs", "remediations", "agent_meta"}
	for _, table := range tables {
		var count int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count)
		assert.NoError(t, err, "table %s should exist", table)
	}

	// Verify agent_meta seeded. schema_version tracks the latest applied
	// migration: 0007 (scan_logs FK drop, issue #52) bumps it to "7".
	v, err := s.GetMeta(ctx, "schema_version")
	require.NoError(t, err)
	assert.Equal(t, "7", v)
}

func TestTargetCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create
	target := &model.Target{Value: "example.com"}
	err := s.CreateTarget(ctx, target)
	require.NoError(t, err)
	assert.NotEmpty(t, target.ID)
	assert.Equal(t, model.TargetTypeDomain, target.Type)
	assert.Equal(t, model.TargetScopeExternal, target.Scope)
	assert.True(t, target.Enabled)

	// Get
	got, err := s.GetTarget(ctx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, target.ID, got.ID)
	assert.Equal(t, "example.com", got.Value)
	assert.Equal(t, model.TargetTypeDomain, got.Type)

	// GetByValue
	got2, err := s.GetTargetByValue(ctx, "example.com")
	require.NoError(t, err)
	assert.Equal(t, target.ID, got2.ID)

	// List
	targets, err := s.ListTargets(ctx)
	require.NoError(t, err)
	assert.Len(t, targets, 1)

	// Delete
	err = s.DeleteTarget(ctx, target.ID)
	require.NoError(t, err)

	_, err = s.GetTarget(ctx, target.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// Delete non-existent
	err = s.DeleteTarget(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTargetAutoDetect(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		value    string
		wantType model.TargetType
	}{
		{"example.com", model.TargetTypeDomain},
		{"sub.example.com", model.TargetTypeDomain},
		{"192.168.1.0/24", model.TargetTypeCIDR},
		{"10.0.0.0/8", model.TargetTypeCIDR},
		{"10.0.0.1", model.TargetTypeIP},
		{"192.168.1.1", model.TargetTypeIP},
	}

	for _, tc := range tests {
		target := &model.Target{Value: tc.value}
		err := s.CreateTarget(ctx, target)
		require.NoError(t, err, "value: %s", tc.value)
		assert.Equal(t, tc.wantType, target.Type, "value: %s", tc.value)
	}
}

func TestTargetValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	invalid := []string{
		"not a valid thing!!!",
		"",
		"://broken",
		"just spaces   ",
	}

	for _, v := range invalid {
		target := &model.Target{Value: v}
		err := s.CreateTarget(ctx, target)
		assert.ErrorIs(t, err, ErrInvalidTarget, "value: %q", v)
	}
}

func TestTargetDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	t1 := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, t1))

	t2 := &model.Target{Value: "example.com"}
	err := s.CreateTarget(ctx, t2)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestUpdateTargetLastScan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	// Initially last_scan_at should be zero
	got, err := s.GetTarget(ctx, target.ID)
	require.NoError(t, err)
	assert.True(t, got.LastScanAt == nil || got.LastScanAt.IsZero())

	// Update last scan
	scanID := "scan-123"
	now := time.Now().UTC().Truncate(time.Second)
	err = s.UpdateTargetLastScan(ctx, target.ID, scanID, now)
	require.NoError(t, err)

	got, err = s.GetTarget(ctx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, scanID, got.LastScanID)
	assert.NotNil(t, got.LastScanAt)
	assert.Equal(t, now, got.LastScanAt.Truncate(time.Second))

	// Non-existent target returns ErrNotFound
	err = s.UpdateTargetLastScan(ctx, "nonexistent", scanID, now)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestScanCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Need a target first
	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	// Create scan
	scan := &model.Scan{
		TargetID: target.ID,
		Type:     model.ScanTypeFull,
		Status:   model.ScanStatusQueued,
	}
	require.NoError(t, s.CreateScan(ctx, scan))
	assert.NotEmpty(t, scan.ID)

	// Get
	got, err := s.GetScan(ctx, scan.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusQueued, got.Status)

	// Update
	now := time.Now().UTC()
	scan.Status = model.ScanStatusRunning
	scan.Phase = "discovery"
	scan.Progress = 25.0
	scan.StartedAt = &now
	scan.TargetState = model.TargetState{
		AssetsByType: map[model.AssetType]int{model.AssetTypeSubdomain: 10},
		AssetsTotal:  10,
	}
	require.NoError(t, s.UpdateScan(ctx, scan))

	got, err = s.GetScan(ctx, scan.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusRunning, got.Status)
	assert.Equal(t, "discovery", got.Phase)
	assert.Equal(t, float32(25.0), got.Progress)
	assert.Equal(t, 10, got.TargetState.AssetsByType[model.AssetTypeSubdomain])
	assert.Equal(t, 10, got.TargetState.AssetsTotal)

	// List
	scans, err := s.ListScans(ctx, target.ID, 10)
	require.NoError(t, err)
	assert.Len(t, scans, 1)

	// List all
	scans, err = s.ListScans(ctx, "", 10)
	require.NoError(t, err)
	assert.Len(t, scans, 1)
}

func TestAssetUpsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	// Insert
	asset := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "sub.example.com",
		Status:   model.AssetStatusNew,
	}
	require.NoError(t, s.UpsertAsset(ctx, asset))
	firstID := asset.ID
	assert.NotEmpty(t, firstID)

	// Verify first_seen is set
	assets, err := s.ListAssets(ctx, AssetListOptions{TargetID: target.ID})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	firstSeen := assets[0].FirstSeen

	// Upsert again — should update last_seen but keep first_seen
	time.Sleep(10 * time.Millisecond)
	asset2 := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "sub.example.com",
		Status:   model.AssetStatusActive,
		Metadata: map[string]interface{}{"updated": true},
	}
	require.NoError(t, s.UpsertAsset(ctx, asset2))

	assets, err = s.ListAssets(ctx, AssetListOptions{TargetID: target.ID})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	// first_seen should be preserved (the ON CONFLICT keeps the original row's first_seen)
	assert.Equal(t, firstSeen, assets[0].FirstSeen)
	assert.Equal(t, model.AssetStatusActive, assets[0].Status)
}

func TestFindingUpsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	asset := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "vuln.example.com",
		Status:   model.AssetStatusActive,
	}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	// Get asset ID from list (upsert may generate new ID)
	assets, _ := s.ListAssets(ctx, AssetListOptions{TargetID: target.ID})
	assetID := assets[0].ID

	// Insert finding
	f := &model.Finding{
		AssetID:    assetID,
		TemplateID: "CVE-2024-1234",
		Severity:   model.SeverityHigh,
		Title:      "Test Vuln",
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 80,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	findings, err := s.ListFindings(ctx, FindingListOptions{AssetID: assetID})
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, model.SeverityHigh, findings[0].Severity)
	firstSeen := findings[0].FirstSeen

	// Upsert with updated severity — dedup by (asset_id, template_id, source_tool)
	time.Sleep(10 * time.Millisecond)
	f2 := &model.Finding{
		AssetID:    assetID,
		TemplateID: "CVE-2024-1234",
		Severity:   model.SeverityCritical,
		Title:      "Test Vuln Updated",
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Evidence:   "new evidence",
		Confidence: 95,
	}
	require.NoError(t, s.UpsertFinding(ctx, f2))

	findings, err = s.ListFindings(ctx, FindingListOptions{AssetID: assetID})
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, model.SeverityCritical, findings[0].Severity)
	assert.Equal(t, "new evidence", findings[0].Evidence)
	// first_seen preserved
	assert.Equal(t, firstSeen, findings[0].FirstSeen)
}

func TestFindingStatusUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	asset := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "test.example.com",
		Status:   model.AssetStatusActive,
	}
	require.NoError(t, s.UpsertAsset(ctx, asset))
	assets, _ := s.ListAssets(ctx, AssetListOptions{TargetID: target.ID})
	assetID := assets[0].ID

	f := &model.Finding{
		AssetID:    assetID,
		TemplateID: "TEST-001",
		Severity:   model.SeverityMedium,
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 50,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	// Update to resolved
	findings, _ := s.ListFindings(ctx, FindingListOptions{AssetID: assetID})
	findingID := findings[0].ID

	require.NoError(t, s.UpdateFindingStatus(ctx, findingID, model.FindingStatusResolved))

	findings, _ = s.ListFindings(ctx, FindingListOptions{AssetID: assetID, Status: model.FindingStatusResolved})
	require.Len(t, findings, 1)
	assert.Equal(t, model.FindingStatusResolved, findings[0].Status)
	assert.NotNil(t, findings[0].ResolvedAt)
}

func TestCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	// Create asset under target
	asset := &model.Asset{
		TargetID: target.ID,
		Type:     model.AssetTypeSubdomain,
		Value:    "sub.example.com",
		Status:   model.AssetStatusActive,
	}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	// Create scan under target
	scan := &model.Scan{
		TargetID: target.ID,
		Type:     model.ScanTypeFull,
		Status:   model.ScanStatusCompleted,
	}
	require.NoError(t, s.CreateScan(ctx, scan))

	// Create finding under asset
	assets, _ := s.ListAssets(ctx, AssetListOptions{TargetID: target.ID})
	f := &model.Finding{
		AssetID:    assets[0].ID,
		ScanID:     scan.ID,
		TemplateID: "TEST-CASCADE",
		Severity:   model.SeverityLow,
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 50,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	// Verify everything exists
	ac, _ := s.CountAssets(ctx)
	assert.Equal(t, 1, ac)
	sc, _ := s.CountScans(ctx)
	assert.Equal(t, 1, sc)
	fc, _ := s.CountFindings(ctx)
	assert.Equal(t, 1, fc)

	// Delete target — should cascade
	require.NoError(t, s.DeleteTarget(ctx, target.ID))

	ac, _ = s.CountAssets(ctx)
	assert.Equal(t, 0, ac)
	sc, _ = s.CountScans(ctx)
	assert.Equal(t, 0, sc)
	fc, _ = s.CountFindings(ctx)
	assert.Equal(t, 0, fc)
}

func TestCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Initially zero
	tc, _ := s.CountTargets(ctx)
	assert.Equal(t, 0, tc)
	sc, _ := s.CountScans(ctx)
	assert.Equal(t, 0, sc)
	fc, _ := s.CountFindings(ctx)
	assert.Equal(t, 0, fc)
	ac, _ := s.CountAssets(ctx)
	assert.Equal(t, 0, ac)

	// LastScan returns nil when no scans
	last, err := s.LastScan(ctx)
	require.NoError(t, err)
	assert.Nil(t, last)

	// Add data
	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	scan := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusCompleted}
	require.NoError(t, s.CreateScan(ctx, scan))

	asset := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "a.example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	tc, _ = s.CountTargets(ctx)
	assert.Equal(t, 1, tc)
	sc, _ = s.CountScans(ctx)
	assert.Equal(t, 1, sc)
	ac, _ = s.CountAssets(ctx)
	assert.Equal(t, 1, ac)

	last, err = s.LastScan(ctx)
	require.NoError(t, err)
	assert.NotNil(t, last)
	assert.Equal(t, scan.ID, last.ID)
}
