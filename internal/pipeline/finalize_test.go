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
