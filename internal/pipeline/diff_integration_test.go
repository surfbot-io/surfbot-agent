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

func sumAssetTypeMap(m map[model.AssetType]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

func sumSeverityMap(m map[model.Severity]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

func TestDiffIntegrationInPipeline(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	// Run 1: discover initial assets
	tools1 := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub1.example.com", Status: model.AssetStatusNew},
				{Type: model.AssetTypeSubdomain, Value: "sub2.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
	}
	reg1 := mockRegistry(tools1...)
	pipe1 := New(s, reg1)

	result1, err := pipe1.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeDiscovery})
	require.NoError(t, err)

	// First scan should be baseline
	assert.True(t, result1.Delta.IsBaseline)
	// Baseline: 3 assets discovered (2 subs + 1 ip)
	assert.Equal(t, 3, result1.TargetState.AssetsTotal)

	// Run 2: different assets (sub1 still present, sub2 gone, sub3 new)
	tools2 := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub1.example.com", Status: model.AssetStatusNew},
				{Type: model.AssetTypeSubdomain, Value: "sub3.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
	}
	reg2 := mockRegistry(tools2...)
	pipe2 := New(s, reg2)

	result2, err := pipe2.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeDiscovery})
	require.NoError(t, err)

	// Should detect changes
	assert.False(t, result2.Delta.IsBaseline)
	assert.NotZero(t, sumAssetTypeMap(result2.Delta.NewAssets), "should have new assets")
	assert.NotZero(t, sumAssetTypeMap(result2.Delta.DisappearedAssets), "should have disappeared assets")

	// Verify changes were persisted
	changes, err := s.ListAssetChanges(ctx, storage.AssetChangeListOptions{ScanID: result2.ScanID, Limit: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, changes)

	// Verify disappeared asset has correct status
	allAssets, _ := s.ListAssets(ctx, storage.AssetListOptions{TargetID: target.ID, Limit: 100})
	for _, a := range allAssets {
		if a.Value == "sub2.example.com" {
			assert.Equal(t, model.AssetStatusDisappeared, a.Status)
		}
	}
}

func TestFindingAutoResolveInPipeline(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	// Run 1: produce a finding
	tools1 := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
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
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "nuclei",
			phase: "assessment",
			findings: []model.Finding{
				{
					TemplateID: "CVE-2024-0001",
					Severity:   model.SeverityHigh,
					Title:      "Test vuln",
					Status:     model.FindingStatusOpen,
					SourceTool: "nuclei",
					Confidence: 90,
				},
			},
		},
	}
	reg1 := mockRegistry(tools1...)
	pipe1 := New(s, reg1)
	_, err := pipe1.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// Verify finding exists and is open
	findings, _ := s.ListFindings(ctx, storage.FindingListOptions{Status: model.FindingStatusOpen, Limit: 10})
	require.NotEmpty(t, findings, "should have open findings after first scan")

	// Run 2: same assets, but NO findings from nuclei
	tools2 := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
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
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "nuclei", phase: "assessment"}, // No findings
	}
	reg2 := mockRegistry(tools2...)
	pipe2 := New(s, reg2)
	result2, err := pipe2.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// Finding should now be auto-resolved
	assert.NotZero(t, sumSeverityMap(result2.Delta.ResolvedFindings), "should have resolved findings")

	// Check in DB
	resolved, _ := s.ListFindings(ctx, storage.FindingListOptions{Status: model.FindingStatusResolved, Limit: 10})
	assert.NotEmpty(t, resolved, "should have resolved findings in DB")
	for _, f := range resolved {
		assert.NotNil(t, f.ResolvedAt, "resolved finding should have resolved_at timestamp")
	}
}
