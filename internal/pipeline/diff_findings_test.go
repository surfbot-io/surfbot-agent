package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func createScanWithTime(t *testing.T, s *storage.SQLiteStore, targetID string, startedAt time.Time) *model.Scan {
	t.Helper()
	scan := &model.Scan{
		TargetID:  targetID,
		Type:      model.ScanTypeFull,
		Status:    model.ScanStatusCompleted,
		StartedAt: &startedAt,
	}
	require.NoError(t, s.CreateScan(context.Background(), scan))
	return scan
}

func TestComputeFindingChangesNew(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	scan := createScanWithTime(t, s, target.ID, time.Now().Add(-1*time.Minute))

	// Create an asset to attach findings to
	a := &model.Asset{TargetID: target.ID, Type: model.AssetTypeURL, Value: "https://example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, a))

	// Create a finding with first_seen == last_seen (new)
	now := time.Now().UTC()
	f := &model.Finding{
		AssetID:    a.ID,
		ScanID:     scan.ID,
		TemplateID: "CVE-2024-0001",
		Severity:   model.SeverityHigh,
		Title:      "New vuln",
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 90,
		FirstSeen:  now,
		LastSeen:   now,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	newFindings, resolvedFindings, err := ComputeFindingChanges(ctx, s, target.ID, scan.ID)
	require.NoError(t, err)

	assert.Len(t, newFindings, 1)
	assert.Equal(t, "CVE-2024-0001", newFindings[0].TemplateID)
	assert.Empty(t, resolvedFindings)
}

func TestComputeFindingChangesResolved(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	scan := createScanWithTime(t, s, target.ID, time.Now().Add(-1*time.Minute))

	a := &model.Asset{TargetID: target.ID, Type: model.AssetTypeURL, Value: "https://example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, a))

	// Create an open finding NOT associated with this scan
	oldTime := time.Now().Add(-1 * time.Hour)
	f := &model.Finding{
		AssetID:    a.ID,
		ScanID:     "", // not tied to current scan
		TemplateID: "CVE-2024-0002",
		Severity:   model.SeverityMedium,
		Title:      "Old vuln",
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 80,
		FirstSeen:  oldTime,
		LastSeen:   oldTime,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	// Current scan has no findings at all
	newFindings, resolvedFindings, err := ComputeFindingChanges(ctx, s, target.ID, scan.ID)
	require.NoError(t, err)

	assert.Empty(t, newFindings)
	assert.Len(t, resolvedFindings, 1)
	assert.Equal(t, "CVE-2024-0002", resolvedFindings[0].TemplateID)
}

func TestAutoResolveFindings(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	a := &model.Asset{TargetID: target.ID, Type: model.AssetTypeURL, Value: "https://example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, a))

	f := &model.Finding{
		AssetID:    a.ID,
		TemplateID: "CVE-2024-0001",
		Severity:   model.SeverityHigh,
		Title:      "Vuln to resolve",
		Status:     model.FindingStatusOpen,
		SourceTool: "nuclei",
		Confidence: 90,
	}
	require.NoError(t, s.UpsertFinding(ctx, f))

	// Auto-resolve
	require.NoError(t, AutoResolveFindings(ctx, s, []model.Finding{*f}))

	// Check finding is resolved
	findings, err := s.ListFindings(ctx, storage.FindingListOptions{Status: model.FindingStatusResolved, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, findings, 1)
	assert.Equal(t, model.FindingStatusResolved, findings[0].Status)
	assert.NotNil(t, findings[0].ResolvedAt)
}
