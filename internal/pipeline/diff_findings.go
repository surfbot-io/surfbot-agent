package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// ComputeFindingChanges identifies new and resolved findings for a scan.
// New findings: first_seen == last_seen (just appeared).
// Resolved findings: open findings not seen in the current scan.
func ComputeFindingChanges(ctx context.Context, store storage.Store, targetID, scanID string) (newFindings, resolvedFindings []model.Finding, err error) {
	// Get current scan's findings
	currentFindings, err := store.ListFindings(ctx, storage.FindingListOptions{ScanID: scanID, Limit: 10000})
	if err != nil {
		return nil, nil, fmt.Errorf("listing current findings: %w", err)
	}

	// Get all open findings (across all scans for this target's assets)
	allOpenFindings, err := store.ListFindings(ctx, storage.FindingListOptions{
		Status: model.FindingStatusOpen,
		Limit:  10000,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("listing open findings: %w", err)
	}

	// Build set of current finding keys: (asset_id, template_id, source_tool)
	type findingKey struct {
		assetID    string
		templateID string
		sourceTool string
	}
	currentKeys := make(map[findingKey]bool, len(currentFindings))
	for _, f := range currentFindings {
		currentKeys[findingKey{f.AssetID, f.TemplateID, f.SourceTool}] = true
	}

	// New findings: first_seen == last_seen (just appeared in this scan)
	for _, f := range currentFindings {
		if f.FirstSeen.Equal(f.LastSeen) {
			newFindings = append(newFindings, f)
		}
	}

	// Resolved findings: open findings whose key is NOT in current scan
	for _, f := range allOpenFindings {
		key := findingKey{f.AssetID, f.TemplateID, f.SourceTool}
		if !currentKeys[key] {
			resolvedFindings = append(resolvedFindings, f)
		}
	}

	return newFindings, resolvedFindings, nil
}

// AutoResolveFindings marks resolved findings as resolved with a timestamp.
func AutoResolveFindings(ctx context.Context, store storage.Store, resolved []model.Finding) error {
	now := time.Now().UTC()
	for _, f := range resolved {
		store.UpdateFindingStatus(ctx, f.ID, model.FindingStatusResolved) //nolint:errcheck
		store.UpdateFindingResolvedAt(ctx, f.ID, &now)                   //nolint:errcheck
	}
	return nil
}
