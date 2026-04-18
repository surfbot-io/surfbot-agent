package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// TestFindingDedupMatchesTargetState is the SPEC-QA3 acceptance test: when
// nuclei emits duplicate findings (same asset + template_id), storage
// merges them. target_state.findings_open_total must equal the DB COUNT,
// not the pre-dedup emission count.
//
// This asserts the end-to-end invariant: stats = DB ground truth.
func TestFindingDedupMatchesTargetState(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	// Nuclei emits 5 findings but only 3 are unique on (asset, template_id,
	// source_tool). Duplicates will merge at storage via the UNIQUE
	// constraint → 3 distinct rows.
	tools := []detection.DetectionTool{
		&mockTool{
			name: "subfinder", phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "naabu", phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "open"}},
			},
		},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name: "nuclei", phase: "assessment",
			findings: []model.Finding{
				{TemplateID: "CVE-2024-A", Severity: model.SeverityHigh, Title: "A", Status: model.FindingStatusOpen, SourceTool: "nuclei"},
				{TemplateID: "CVE-2024-A", Severity: model.SeverityHigh, Title: "A", Status: model.FindingStatusOpen, SourceTool: "nuclei"}, // dup
				{TemplateID: "CVE-2024-B", Severity: model.SeverityMedium, Title: "B", Status: model.FindingStatusOpen, SourceTool: "nuclei"},
				{TemplateID: "CVE-2024-B", Severity: model.SeverityMedium, Title: "B", Status: model.FindingStatusOpen, SourceTool: "nuclei"}, // dup
				{TemplateID: "CVE-2024-C", Severity: model.SeverityCritical, Title: "C", Status: model.FindingStatusOpen, SourceTool: "nuclei"},
			},
		},
	}

	reg := mockRegistry(tools...)
	pipe := New(s, reg)
	result, err := pipe.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// DB ground truth: exactly 3 findings persisted (duplicates merged).
	dbFindings, err := s.ListFindings(ctx, storage.FindingListOptions{TargetID: target.ID, Limit: 100})
	require.NoError(t, err)
	assert.Len(t, dbFindings, 3, "storage must dedup on (asset, template_id, source_tool)")

	// target_state must equal DB ground truth — the whole point of QA3.
	assert.Equal(t, 3, result.TargetState.FindingsOpenTotal,
		"findings_open_total must equal the persisted row count, not the pre-dedup emission count")
	assert.Equal(t, 1, result.TargetState.FindingsOpen[model.SeverityCritical])
	assert.Equal(t, 1, result.TargetState.FindingsOpen[model.SeverityHigh])
	assert.Equal(t, 1, result.TargetState.FindingsOpen[model.SeverityMedium])

	// Re-reading the scan row from the DB must yield the same snapshot —
	// the aggregate is persisted, not computed fresh each time.
	persisted, err := s.GetScan(ctx, result.ScanID)
	require.NoError(t, err)
	assert.Equal(t, 3, persisted.TargetState.FindingsOpenTotal)

	// work.raw_emissions captures the pre-dedup tool output — 5 emissions
	// before storage merged them into 3. Useful for debugging tool noise.
	assert.Equal(t, 5, result.Work.RawEmissions,
		"work.raw_emissions must record pre-dedup tool emissions")
}

// TestWorkPhasesRunInExecutionOrder guards against the regression where
// FinalizeScanWork sorted phases alphabetically — that produced
// "assessment → discovery → http_probe → port_scan → resolution" for a
// scan whose actual pipeline order is "discovery → resolution →
// port_scan → http_probe → assessment". An LLM consumer reading that
// JSON would infer the wrong execution order.
func TestWorkPhasesRunInExecutionOrder(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	tools := []detection.DetectionTool{
		&mockTool{name: "subfinder", phase: "discovery",
			assets: []model.Asset{{Type: model.AssetTypeSubdomain, Value: "a.example.com", Status: model.AssetStatusNew}}},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew}}},
		&mockTool{name: "naabu", phase: "port_scan",
			assets: []model.Asset{{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "open"}}}},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{{Type: model.AssetTypeURL, Value: "https://a.example.com", Status: model.AssetStatusNew}}},
		&mockTool{name: "nuclei", phase: "assessment"},
	}

	reg := mockRegistry(tools...)
	pipe := New(s, reg)
	result, err := pipe.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	expected := []string{"discovery", "resolution", "port_scan", "http_probe", "assessment"}
	assert.Equal(t, expected, result.Work.PhasesRun,
		"phases_run must reflect actual pipeline execution order, not alphabetic")
}

// TestTargetStatePortsBucket asserts that port_scan assets with different
// metadata.status values land in the correct target_state.ports_by_status
// buckets. Previously SPEC-QA2 introduced status=filtered ports but the old
// stats conflated open + filtered into a single OpenPorts count.
func TestTargetStatePortsBucket(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	tools := []detection.DetectionTool{
		&mockTool{name: "subfinder", phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "naabu", phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:80/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "open"}},
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "open"}},
				{Type: model.AssetTypePort, Value: "1.2.3.4:8080/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "filtered"}},
			},
		},
	}

	reg := mockRegistry(tools...)
	pipe := New(s, reg)
	result, err := pipe.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	assert.Equal(t, 2, result.TargetState.PortsByStatus["open"], "open ports bucketed separately")
	assert.Equal(t, 1, result.TargetState.PortsByStatus["filtered"], "filtered ports bucketed separately")
}

// TestBaselineDeltaIsEmpty guards the SUR-244 P0 invariant: when a scan
// is a baseline (first run against a target), the delta payload is
// uniformly empty across all buckets, regardless of what the scan
// observed. Consumers key off is_baseline=true to know "all state here
// is new"; populating delta.new_findings while leaving delta.new_assets
// empty (the pre-fix behavior) produced an inconsistent shape that
// LLMs and dashboards had to special-case.
func TestBaselineDeltaIsEmpty(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	// Baseline scan that also produces a finding. Pre-fix this would
	// yield delta.new_findings = {high: 1} while delta.new_assets = {}.
	tools := []detection.DetectionTool{
		&mockTool{name: "subfinder", phase: "discovery",
			assets: []model.Asset{{Type: model.AssetTypeSubdomain, Value: "a.example.com", Status: model.AssetStatusNew}}},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew}}},
		&mockTool{name: "naabu", phase: "port_scan",
			assets: []model.Asset{{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew, Metadata: map[string]any{"status": "open"}}}},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{{Type: model.AssetTypeURL, Value: "https://a.example.com", Status: model.AssetStatusNew}}},
		&mockTool{
			name: "nuclei", phase: "assessment",
			findings: []model.Finding{
				{TemplateID: "CVE-X", Severity: model.SeverityHigh, Title: "High vuln", Status: model.FindingStatusOpen, SourceTool: "nuclei"},
			},
		},
	}

	reg := mockRegistry(tools...)
	pipe := New(s, reg)
	result, err := pipe.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	d := result.Delta
	assert.True(t, d.IsBaseline, "first scan must be flagged baseline")
	assert.Empty(t, d.NewAssets, "baseline: new_assets must be empty")
	assert.Empty(t, d.DisappearedAssets, "baseline: disappeared_assets must be empty")
	assert.Empty(t, d.ModifiedAssets, "baseline: modified_assets must be empty")
	assert.Empty(t, d.NewFindings, "baseline: new_findings must be empty (was populated pre-fix)")
	assert.Empty(t, d.ResolvedFindings, "baseline: resolved_findings must be empty")
	assert.Empty(t, d.ReturnedFindings, "baseline: returned_findings must be empty")

	// Target State is how the consumer should learn about what exists
	// after a baseline scan — verify the finding shows up there.
	assert.Equal(t, 1, result.TargetState.FindingsOpenTotal,
		"target_state must reflect what the baseline observed (single source of truth for baselines)")
}

// TestFindingsScanScopeIncludesDiscovered covers the storage-level half of
// the SUR-244 P0 fix: a finding observed by scan1 and re-observed by
// scan2 must stay visible in scan1's detail view. ScanScope=scan1 must
// match both findings that scan1 observed last (scan_id=scan1) AND
// findings that scan1 discovered originally (first_seen_scan_id=scan1),
// regardless of who observed them last.
func TestFindingsScanScopeIncludesDiscovered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	scan1 := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan1))
	scan2 := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan2))

	asset := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "a.example.com", Status: model.AssetStatusNew}
	require.NoError(t, s.UpsertAsset(ctx, asset))

	// scan1 discovers the finding.
	require.NoError(t, s.UpsertFinding(ctx, &model.Finding{
		AssetID: asset.ID, ScanID: scan1.ID,
		TemplateID: "T-1", SourceTool: "nuclei",
		Severity: model.SeverityHigh, Title: "v", Status: model.FindingStatusOpen,
	}))
	// scan2 re-observes it → scan_id flips to scan2, first_seen_scan_id
	// stays on scan1 (tested separately in storage/scan_aggregates_test.go).
	require.NoError(t, s.UpsertFinding(ctx, &model.Finding{
		AssetID: asset.ID, ScanID: scan2.ID,
		TemplateID: "T-1", SourceTool: "nuclei",
		Severity: model.SeverityHigh, Title: "v", Status: model.FindingStatusOpen,
	}))

	// Stricter ScanID filter: scan1 has nothing (scan_id now points at scan2).
	byScan1ID, err := s.ListFindings(ctx, storage.FindingListOptions{ScanID: scan1.ID, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, byScan1ID, "ScanID=scan1 returns empty after re-observation — expected")

	// Broader ScanScope filter: scan1 still owns the finding via
	// first_seen_scan_id. This is what the scan-detail UI should use.
	byScan1Scope, err := s.ListFindings(ctx, storage.FindingListOptions{ScanScope: scan1.ID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, byScan1Scope, 1, "ScanScope=scan1 must include findings scan1 originally discovered")

	// scan2 picks it up via either filter (it's the latest observer AND
	// its ScanScope includes the finding via scan_id match).
	byScan2Scope, err := s.ListFindings(ctx, storage.FindingListOptions{ScanScope: scan2.ID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, byScan2Scope, 1, "ScanScope=scan2 must include findings scan2 re-observed")
}
