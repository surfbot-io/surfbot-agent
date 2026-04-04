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

func TestComputeChangesBaseline(t *testing.T) {
	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "sub1.example.com"},
		{ID: "a2", Type: model.AssetTypeIPv4, Value: "1.2.3.4"},
	}

	changes := ComputeChanges("target1", "scan1", nil, postAssets, true)

	assert.Len(t, changes, 2)
	for _, c := range changes {
		assert.Equal(t, model.ChangeTypeAppeared, c.ChangeType)
		assert.Equal(t, model.SignificanceInfo, c.Significance)
		assert.True(t, c.Baseline)
	}
}

func TestComputeChangesAppeared(t *testing.T) {
	preAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "old.example.com"},
	}

	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "old.example.com"},
		{ID: "a2", Type: model.AssetTypeSubdomain, Value: "new.example.com"},
		{ID: "a3", Type: model.AssetTypeIPv4, Value: "5.6.7.8"},
		{ID: "a4", Type: model.AssetTypeTechnology, Value: "nginx"},
	}

	changes := ComputeChanges("target1", "scan2", preAssets, postAssets, false)
	appeared := filterChanges(changes, model.ChangeTypeAppeared)

	assert.Len(t, appeared, 3)
	for _, c := range appeared {
		switch model.AssetType(c.AssetType) {
		case model.AssetTypeSubdomain:
			assert.Equal(t, model.SignificanceCritical, c.Significance)
		case model.AssetTypeIPv4:
			assert.Equal(t, model.SignificanceHigh, c.Significance)
		case model.AssetTypeTechnology:
			assert.Equal(t, model.SignificanceLow, c.Significance)
		}
	}
}

func TestComputeChangesDisappeared(t *testing.T) {
	preAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "old.example.com"},
		{ID: "a2", Type: model.AssetTypeSubdomain, Value: "still.example.com"},
	}
	// "old" still in DB but last_seen not updated (same as pre)
	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "old.example.com"},         // same last_seen → disappeared
		{ID: "a2", Type: model.AssetTypeSubdomain, Value: "still.example.com"},        // will have updated last_seen
	}
	// Simulate: "still" was touched by the scan (different LastSeen), "old" was not
	preAssets[0].LastSeen = parseTestTime("2024-01-01T00:00:00Z")
	preAssets[1].LastSeen = parseTestTime("2024-01-01T00:00:00Z")
	postAssets[0].LastSeen = parseTestTime("2024-01-01T00:00:00Z") // NOT updated
	postAssets[1].LastSeen = parseTestTime("2024-01-02T00:00:00Z") // Updated by scan

	changes := ComputeChanges("target1", "scan2", preAssets, postAssets, false)
	disappeared := filterChanges(changes, model.ChangeTypeDisappeared)

	assert.Len(t, disappeared, 1)
	assert.Equal(t, "old.example.com", disappeared[0].AssetValue)
	assert.Equal(t, model.SignificanceHigh, disappeared[0].Significance)
}

func TestComputeChangesModified(t *testing.T) {
	preAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeURL, Value: "https://api.example.com",
			Metadata:  map[string]any{"server": "nginx/1.20"},
			LastSeen: parseTestTime("2024-01-01T00:00:00Z"),
		},
	}
	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeURL, Value: "https://api.example.com",
			Metadata:  map[string]any{"server": "nginx/1.25"},
			LastSeen: parseTestTime("2024-01-02T00:00:00Z"),
		},
	}

	changes := ComputeChanges("target1", "scan2", preAssets, postAssets, false)
	modified := filterChanges(changes, model.ChangeTypeModified)

	assert.Len(t, modified, 1)
	assert.Equal(t, model.SignificanceCritical, modified[0].Significance)
	assert.Contains(t, modified[0].Summary, "Server version changed")
}

func TestComputeChangesMixed(t *testing.T) {
	preAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "removed.example.com",
			LastSeen: parseTestTime("2024-01-01T00:00:00Z")},
		{ID: "a2", Type: model.AssetTypeURL, Value: "https://api.example.com",
			Metadata: map[string]any{"server": "nginx/1.20"},
			LastSeen: parseTestTime("2024-01-01T00:00:00Z")},
	}
	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "removed.example.com",
			LastSeen: parseTestTime("2024-01-01T00:00:00Z")}, // not updated → disappeared
		{ID: "a2", Type: model.AssetTypeURL, Value: "https://api.example.com",
			Metadata: map[string]any{"server": "nginx/1.25"},
			LastSeen: parseTestTime("2024-01-02T00:00:00Z")}, // updated + modified
		{ID: "a3", Type: model.AssetTypeSubdomain, Value: "new1.example.com",
			LastSeen: parseTestTime("2024-01-02T00:00:00Z")}, // new
		{ID: "a4", Type: model.AssetTypeIPv4, Value: "9.8.7.6",
			LastSeen: parseTestTime("2024-01-02T00:00:00Z")}, // new
	}

	changes := ComputeChanges("target1", "scan2", preAssets, postAssets, false)

	appeared := filterChanges(changes, model.ChangeTypeAppeared)
	disappeared := filterChanges(changes, model.ChangeTypeDisappeared)
	modified := filterChanges(changes, model.ChangeTypeModified)

	assert.Len(t, appeared, 2, "should have 2 appeared")
	assert.Len(t, disappeared, 1, "should have 1 disappeared")
	assert.Len(t, modified, 1, "should have 1 modified")
}

func TestComputeChangesNoChange(t *testing.T) {
	ts := parseTestTime("2024-01-01T00:00:00Z")
	ts2 := parseTestTime("2024-01-02T00:00:00Z")

	preAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "same.example.com", LastSeen: ts},
	}
	postAssets := []model.Asset{
		{ID: "a1", Type: model.AssetTypeSubdomain, Value: "same.example.com", LastSeen: ts2},
	}

	changes := ComputeChanges("target1", "scan2", preAssets, postAssets, false)
	assert.Empty(t, changes)
}

func TestClassifyMetadataChangeServerVersion(t *testing.T) {
	prev := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"server": "nginx/1.20"}}
	curr := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"server": "nginx/1.25"}}

	change, modified := classifyMetadataChange(prev, curr)
	assert.True(t, modified)
	assert.Equal(t, model.SignificanceCritical, change.Significance)
	assert.Contains(t, change.Summary, "Server version changed")
}

func TestClassifyMetadataChangeCNAME(t *testing.T) {
	prev := model.Asset{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Metadata: map[string]any{"cname": "old.cdn.com"}}
	curr := model.Asset{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Metadata: map[string]any{"cname": "new.cdn.com"}}

	change, modified := classifyMetadataChange(prev, curr)
	assert.True(t, modified)
	assert.Equal(t, model.SignificanceCritical, change.Significance)
	assert.Contains(t, change.Summary, "CNAME changed")
}

func TestClassifyMetadataChangeStatusCode(t *testing.T) {
	prev := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"status_code": "200"}}
	curr := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"status_code": "403"}}

	change, modified := classifyMetadataChange(prev, curr)
	assert.True(t, modified)
	assert.Equal(t, model.SignificanceMedium, change.Significance)
	assert.Contains(t, change.Summary, "HTTP status changed")
}

func TestClassifyMetadataChangeTitleOnly(t *testing.T) {
	prev := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"title": "Old Title"}}
	curr := model.Asset{Type: model.AssetTypeURL, Value: "https://example.com", Metadata: map[string]any{"title": "New Title"}}

	change, modified := classifyMetadataChange(prev, curr)
	assert.True(t, modified)
	assert.Equal(t, model.SignificanceInfo, change.Significance)
	assert.Contains(t, change.Summary, "Page title changed")
}

func TestClassifySignificance(t *testing.T) {
	assert.Equal(t, model.SignificanceCritical, classifySignificance(model.ChangeTypeAppeared, model.AssetTypeSubdomain))
	assert.Equal(t, model.SignificanceCritical, classifySignificance(model.ChangeTypeAppeared, model.AssetTypeDomain))
	assert.Equal(t, model.SignificanceHigh, classifySignificance(model.ChangeTypeAppeared, model.AssetTypeIPv4))
	assert.Equal(t, model.SignificanceCritical, classifySignificance(model.ChangeTypeAppeared, model.AssetTypePort))
	assert.Equal(t, model.SignificanceMedium, classifySignificance(model.ChangeTypeAppeared, model.AssetTypeURL))
	assert.Equal(t, model.SignificanceLow, classifySignificance(model.ChangeTypeAppeared, model.AssetTypeTechnology))

	assert.Equal(t, model.SignificanceHigh, classifySignificance(model.ChangeTypeDisappeared, model.AssetTypeSubdomain))
	assert.Equal(t, model.SignificanceMedium, classifySignificance(model.ChangeTypeDisappeared, model.AssetTypeIPv4))
	assert.Equal(t, model.SignificanceHigh, classifySignificance(model.ChangeTypeDisappeared, model.AssetTypePort))
	assert.Equal(t, model.SignificanceLow, classifySignificance(model.ChangeTypeDisappeared, model.AssetTypeTechnology))
}

func TestApplyStatusChanges(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	a := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "test.example.com", Status: model.AssetStatusActive}
	require.NoError(t, s.UpsertAsset(ctx, a))

	changes := []model.AssetChange{
		{AssetID: a.ID, ChangeType: model.ChangeTypeDisappeared},
	}
	require.NoError(t, ApplyStatusChanges(ctx, s, changes))

	assets, _ := s.ListAssets(ctx, storage.AssetListOptions{TargetID: target.ID, Limit: 10})
	found := false
	for _, asset := range assets {
		if asset.Value == "test.example.com" {
			assert.Equal(t, model.AssetStatusDisappeared, asset.Status)
			found = true
		}
	}
	assert.True(t, found)
}

func TestNormalizeAssetStatuses(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")
	ctx := context.Background()

	a := &model.Asset{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "new.example.com", Status: model.AssetStatusNew}
	require.NoError(t, s.UpsertAsset(ctx, a))

	require.NoError(t, s.NormalizeAssetStatuses(ctx, target.ID))

	assets, _ := s.ListAssets(ctx, storage.AssetListOptions{TargetID: target.ID, Limit: 10})
	for _, asset := range assets {
		if asset.Value == "new.example.com" {
			assert.Equal(t, model.AssetStatusActive, asset.Status)
		}
	}
}

func TestBuildChangeSummary(t *testing.T) {
	changes := []model.AssetChange{
		{ChangeType: model.ChangeTypeAppeared, AssetType: "subdomain"},
		{ChangeType: model.ChangeTypeAppeared, AssetType: "subdomain"},
		{ChangeType: model.ChangeTypeAppeared, AssetType: "ipv4"},
		{ChangeType: model.ChangeTypeDisappeared, AssetType: "subdomain"},
		{ChangeType: model.ChangeTypeModified, Significance: model.SignificanceCritical},
	}

	summary := BuildChangeSummary(changes, 2, 1)
	assert.Equal(t, 3, summary.NewAssets)
	assert.Equal(t, 1, summary.DisappearedAssets)
	assert.Equal(t, 1, summary.ModifiedAssets)
	assert.Equal(t, 1, summary.CriticalModified)
	assert.Equal(t, 2, summary.NewFindings)
	assert.Equal(t, 1, summary.ResolvedFindings)
	assert.False(t, summary.IsBaseline)
}

func TestBuildChangeSummaryBaseline(t *testing.T) {
	changes := []model.AssetChange{
		{ChangeType: model.ChangeTypeAppeared, Baseline: true},
		{ChangeType: model.ChangeTypeAppeared, Baseline: true},
	}

	summary := BuildChangeSummary(changes, 0, 0)
	assert.True(t, summary.IsBaseline)
	assert.Equal(t, 2, summary.TotalBaselineAssets)
	assert.Equal(t, 0, summary.NewAssets)
}

// --- Helpers ---

func filterChanges(changes []model.AssetChange, ct model.ChangeType) []model.AssetChange {
	var filtered []model.AssetChange
	for _, c := range changes {
		if c.ChangeType == ct && !c.Baseline {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func parseTestTime(s string) (t time.Time) {
	t, _ = time.Parse(time.RFC3339, s)
	return t
}
