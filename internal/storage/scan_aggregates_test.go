package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// TestUpsertFindingScanIDLatestSemantics is the critical invariant of the
// SPEC-QA3 refactor: scan_id must reflect the LATEST scan that observed the
// finding, while first_seen_scan_id remains immutable at the originating
// scan. Before this change, scan_id froze on first insert and lied.
func TestUpsertFindingScanIDLatestSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	scan1 := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan1))
	scan2 := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan2))

	asset := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	// First observation in scan1.
	f1 := &model.Finding{
		AssetID: asset.ID, ScanID: scan1.ID,
		TemplateID: "CVE-2024-0001", SourceTool: "nuclei",
		Severity: model.SeverityHigh, Title: "Vuln", Status: model.FindingStatusOpen,
	}
	require.NoError(t, s.UpsertFinding(ctx, f1))

	// Same finding re-observed in scan2 → scan_id flips to scan2,
	// first_seen_scan_id stays on scan1.
	f2 := &model.Finding{
		AssetID: asset.ID, ScanID: scan2.ID,
		TemplateID: "CVE-2024-0001", SourceTool: "nuclei",
		Severity: model.SeverityHigh, Title: "Vuln", Status: model.FindingStatusOpen,
	}
	require.NoError(t, s.UpsertFinding(ctx, f2))

	got, err := s.GetFinding(ctx, f1.ID)
	require.NoError(t, err)
	assert.Equal(t, scan2.ID, got.ScanID, "scan_id must track the LATEST observing scan")
	assert.Equal(t, scan1.ID, got.FirstSeenScanID, "first_seen_scan_id must be immutable")

	// Querying "findings observed in scan2" must return this finding.
	observedInScan2, err := s.ListFindings(ctx, FindingListOptions{ScanID: scan2.ID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, observedInScan2, 1, "scan2 should find the re-observed finding")

	// Querying "findings observed in scan1" now returns empty — scan1 no
	// longer owns the scan_id pointer. The discovery record survives via
	// first_seen_scan_id.
	observedInScan1, err := s.ListFindings(ctx, FindingListOptions{ScanID: scan1.ID, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, observedInScan1, "scan1 no longer holds the scan_id after scan2 re-observed")
}

// TestCountAssetsByTypeForTargetExcludesDisappeared asserts that the
// target_state aggregate reflects live assets only. Disappeared assets are
// excluded from "what currently exists" by design.
func TestCountAssetsByTypeForTargetExcludesDisappeared(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	alive := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "alive.example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, alive))

	gone := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "gone.example.com", Status: model.AssetStatusDisappeared}
	require.NoError(t, s.UpsertAsset(ctx, gone))
	require.NoError(t, s.UpdateAssetStatus(ctx, gone.ID, model.AssetStatusDisappeared))

	counts, err := s.CountAssetsByTypeForTarget(ctx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, counts[model.AssetTypeSubdomain], "disappeared assets must not count toward live state")
}

// TestCountPortsByStatusForTargetBucketsCorrectly asserts that ports split
// cleanly into their metadata.status buckets. Ports with missing status
// metadata bucket under "unknown" so the total always equals the
// port_service asset count.
func TestCountPortsByStatusForTargetBucketsCorrectly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	cases := []struct {
		value    string
		metadata map[string]any
	}{
		{"1.2.3.4:80/tcp", map[string]any{"status": "open"}},
		{"1.2.3.4:443/tcp", map[string]any{"status": "open"}},
		{"1.2.3.4:8080/tcp", map[string]any{"status": "filtered"}},
		{"1.2.3.4:9000/tcp", map[string]any{}}, // missing status → unknown
	}
	for _, c := range cases {
		asset := &model.Asset{
			TargetID: target.ID, Type: model.AssetTypePort,
			Value: c.value, Status: model.AssetStatusActive,
			Metadata: c.metadata,
		}
		require.NoError(t, s.UpsertAsset(ctx, asset))
	}

	counts, err := s.CountPortsByStatusForTarget(ctx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, counts["open"])
	assert.Equal(t, 1, counts["filtered"])
	assert.Equal(t, 1, counts["unknown"])
}

// TestAssetChangeCountsForScanSkipsBaseline asserts that baseline asset
// changes don't bubble into delta.new_assets. Baselines are flagged
// separately via ScanIsBaseline.
func TestAssetChangeCountsForScanSkipsBaseline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	scan := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan))

	// Baseline change (first scan of this target).
	baselineChange := &model.AssetChange{
		TargetID: target.ID, ScanID: scan.ID,
		ChangeType: model.ChangeTypeAppeared, Significance: model.SignificanceInfo,
		AssetType: string(model.AssetTypeSubdomain), AssetValue: "baseline.example.com",
		Summary: "baseline", Baseline: true,
	}
	require.NoError(t, s.CreateAssetChange(ctx, baselineChange))

	// Real appeared change (genuinely new).
	newChange := &model.AssetChange{
		TargetID: target.ID, ScanID: scan.ID,
		ChangeType: model.ChangeTypeAppeared, Significance: model.SignificanceCritical,
		AssetType: string(model.AssetTypeSubdomain), AssetValue: "newcomer.example.com",
		Summary: "new",
	}
	require.NoError(t, s.CreateAssetChange(ctx, newChange))

	counts, err := s.AssetChangeCountsForScan(ctx, scan.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, counts[string(model.ChangeTypeAppeared)][model.AssetTypeSubdomain],
		"only the non-baseline change should count")

	isBaseline, err := s.ScanIsBaseline(ctx, scan.ID)
	require.NoError(t, err)
	assert.True(t, isBaseline, "ScanIsBaseline must surface the baseline flag independently")
}
