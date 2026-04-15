package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// ComputeChanges compares post-scan assets against a pre-scan snapshot.
// preAssets is captured before tools run; postAssets is captured after.
func ComputeChanges(targetID, scanID string, preAssets, postAssets []model.Asset, isFirstScan bool) []model.AssetChange {
	if isFirstScan {
		return computeBaselineChanges(targetID, scanID, postAssets)
	}

	preMap := buildAssetMap(preAssets)
	postMap := buildAssetMap(postAssets)

	var changes []model.AssetChange

	// Find APPEARED assets (in post, not in pre)
	for key, curr := range postMap {
		if _, existed := preMap[key]; !existed {
			changes = append(changes, model.AssetChange{
				ID:           uuid.New().String(),
				TargetID:     targetID,
				ScanID:       scanID,
				AssetID:      curr.ID,
				ChangeType:   model.ChangeTypeAppeared,
				Significance: classifySignificance(model.ChangeTypeAppeared, curr.Type),
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				CurrentMeta:  curr.Metadata,
				Summary:      generateSummary(model.ChangeTypeAppeared, curr),
			})
		}
	}

	// Find DISAPPEARED assets (in pre, not in post with updated last_seen)
	for key, prev := range preMap {
		if post, exists := postMap[key]; !exists || post.LastSeen.Equal(prev.LastSeen) {
			// Asset not touched by this scan → disappeared
			if _, inPost := postMap[key]; inPost {
				// Asset exists but wasn't updated → disappeared
				changes = append(changes, model.AssetChange{
					ID:           uuid.New().String(),
					TargetID:     targetID,
					ScanID:       scanID,
					AssetID:      prev.ID,
					ChangeType:   model.ChangeTypeDisappeared,
					Significance: classifySignificance(model.ChangeTypeDisappeared, prev.Type),
					AssetType:    string(prev.Type),
					AssetValue:   prev.Value,
					PreviousMeta: prev.Metadata,
					Summary:      generateSummary(model.ChangeTypeDisappeared, prev),
				})
			} else {
				// Asset removed from DB entirely (shouldn't happen with upsert, but handle it)
				changes = append(changes, model.AssetChange{
					ID:           uuid.New().String(),
					TargetID:     targetID,
					ScanID:       scanID,
					AssetID:      prev.ID,
					ChangeType:   model.ChangeTypeDisappeared,
					Significance: classifySignificance(model.ChangeTypeDisappeared, prev.Type),
					AssetType:    string(prev.Type),
					AssetValue:   prev.Value,
					PreviousMeta: prev.Metadata,
					Summary:      generateSummary(model.ChangeTypeDisappeared, prev),
				})
			}
		}
	}

	// Find MODIFIED assets (in both, metadata changed)
	for key, post := range postMap {
		if pre, existed := preMap[key]; existed && !post.LastSeen.Equal(pre.LastSeen) {
			// Asset was updated in this scan — check metadata
			if change, modified := classifyMetadataChange(pre, post); modified {
				change.ID = uuid.New().String()
				change.TargetID = targetID
				change.ScanID = scanID
				change.AssetID = post.ID
				changes = append(changes, change)
			}
		}
	}

	return changes
}

func computeBaselineChanges(targetID, scanID string, assets []model.Asset) []model.AssetChange {
	var changes []model.AssetChange
	for _, a := range assets {
		changes = append(changes, model.AssetChange{
			ID:           uuid.New().String(),
			TargetID:     targetID,
			ScanID:       scanID,
			AssetID:      a.ID,
			ChangeType:   model.ChangeTypeAppeared,
			Significance: model.SignificanceInfo,
			AssetType:    string(a.Type),
			AssetValue:   a.Value,
			CurrentMeta:  a.Metadata,
			Summary:      fmt.Sprintf("Baseline: %s %s", a.Type, a.Value),
			Baseline:     true,
		})
	}
	return changes
}

// SnapshotAssets captures all current assets for a target (for pre-scan state).
func SnapshotAssets(ctx context.Context, store storage.Store, targetID string) ([]model.Asset, error) {
	return store.ListAssets(ctx, storage.AssetListOptions{TargetID: targetID, Limit: 10000})
}

type assetKey struct {
	typ   string
	value string
}

func buildAssetMap(assets []model.Asset) map[assetKey]model.Asset {
	m := make(map[assetKey]model.Asset, len(assets))
	for _, a := range assets {
		m[assetKey{typ: string(a.Type), value: a.Value}] = a
	}
	return m
}

func classifySignificance(changeType model.ChangeType, assetType model.AssetType) model.Significance {
	switch changeType {
	case model.ChangeTypeAppeared:
		switch assetType {
		case model.AssetTypeDomain, model.AssetTypeSubdomain:
			return model.SignificanceCritical
		case model.AssetTypeIPv4, model.AssetTypeIPv6:
			return model.SignificanceHigh
		case model.AssetTypePort:
			return model.SignificanceCritical
		case model.AssetTypeURL:
			return model.SignificanceMedium
		case model.AssetTypeTechnology:
			return model.SignificanceLow
		}
	case model.ChangeTypeDisappeared:
		switch assetType {
		case model.AssetTypeDomain, model.AssetTypeSubdomain:
			return model.SignificanceHigh
		case model.AssetTypeIPv4, model.AssetTypeIPv6:
			return model.SignificanceMedium
		case model.AssetTypePort:
			return model.SignificanceHigh
		default:
			return model.SignificanceLow
		}
	case model.ChangeTypeModified:
		return model.SignificanceInfo
	}
	return model.SignificanceInfo
}

func classifyMetadataChange(prev, curr model.Asset) (model.AssetChange, bool) {
	prevMeta := prev.Metadata
	currMeta := curr.Metadata
	if prevMeta == nil || currMeta == nil {
		return model.AssetChange{}, false
	}

	// Server version change → critical
	if prevServer, ok := prevMeta["server"]; ok {
		if currServer, ok2 := currMeta["server"]; ok2 && fmt.Sprint(prevServer) != fmt.Sprint(currServer) {
			return model.AssetChange{
				ChangeType:   model.ChangeTypeModified,
				Significance: model.SignificanceCritical,
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				PreviousMeta: prevMeta,
				CurrentMeta:  currMeta,
				Summary:      fmt.Sprintf("Server version changed: %v → %v", prevServer, currServer),
			}, true
		}
	}

	// CNAME change → critical
	if prevCNAME, ok := prevMeta["cname"]; ok {
		if currCNAME, ok2 := currMeta["cname"]; ok2 && fmt.Sprint(prevCNAME) != fmt.Sprint(currCNAME) {
			return model.AssetChange{
				ChangeType:   model.ChangeTypeModified,
				Significance: model.SignificanceCritical,
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				PreviousMeta: prevMeta,
				CurrentMeta:  currMeta,
				Summary:      fmt.Sprintf("CNAME changed: %v → %v", prevCNAME, currCNAME),
			}, true
		}
	}

	// Status code change → medium
	if prevStatus, ok := prevMeta["status_code"]; ok {
		if currStatus, ok2 := currMeta["status_code"]; ok2 && fmt.Sprint(prevStatus) != fmt.Sprint(currStatus) {
			return model.AssetChange{
				ChangeType:   model.ChangeTypeModified,
				Significance: model.SignificanceMedium,
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				PreviousMeta: prevMeta,
				CurrentMeta:  currMeta,
				Summary:      fmt.Sprintf("HTTP status changed: %v → %v", prevStatus, currStatus),
			}, true
		}
	}

	// TLS validity change → high
	if prevTLS, ok := prevMeta["tls_valid"]; ok {
		if currTLS, ok2 := currMeta["tls_valid"]; ok2 && fmt.Sprint(prevTLS) != fmt.Sprint(currTLS) {
			return model.AssetChange{
				ChangeType:   model.ChangeTypeModified,
				Significance: model.SignificanceHigh,
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				PreviousMeta: prevMeta,
				CurrentMeta:  currMeta,
				Summary:      fmt.Sprintf("TLS validity changed: %v → %v", prevTLS, currTLS),
			}, true
		}
	}

	// Title change → info
	if prevTitle, ok := prevMeta["title"]; ok {
		if currTitle, ok2 := currMeta["title"]; ok2 && fmt.Sprint(prevTitle) != fmt.Sprint(currTitle) {
			return model.AssetChange{
				ChangeType:   model.ChangeTypeModified,
				Significance: model.SignificanceInfo,
				AssetType:    string(curr.Type),
				AssetValue:   curr.Value,
				PreviousMeta: prevMeta,
				CurrentMeta:  currMeta,
				Summary:      fmt.Sprintf("Page title changed: %q → %q", prevTitle, currTitle),
			}, true
		}
	}

	return model.AssetChange{}, false
}

func generateSummary(changeType model.ChangeType, a model.Asset) string {
	switch changeType {
	case model.ChangeTypeAppeared:
		return fmt.Sprintf("New %s discovered: %s", a.Type, a.Value)
	case model.ChangeTypeDisappeared:
		return fmt.Sprintf("%s no longer detected: %s", strings.Title(string(a.Type)), a.Value) //nolint:staticcheck
	}
	return ""
}

// ApplyStatusChanges updates asset statuses based on computed changes.
func ApplyStatusChanges(ctx context.Context, store storage.Store, changes []model.AssetChange) error {
	for _, c := range changes {
		if c.AssetID == "" {
			continue
		}
		switch c.ChangeType {
		case model.ChangeTypeAppeared:
			store.UpdateAssetStatus(ctx, c.AssetID, model.AssetStatusNew) //nolint:errcheck
		case model.ChangeTypeDisappeared:
			store.UpdateAssetStatus(ctx, c.AssetID, model.AssetStatusDisappeared) //nolint:errcheck
		}
	}
	return nil
}

// PrintChangeSummary prints a human-readable summary of the scan delta to
// stderr. Source of truth is the ScanDelta computed by FinalizeScanDelta —
// there is no separate ChangeSummary struct; the delta IS the summary.
func PrintChangeSummary(delta model.ScanDelta) {
	success := color.RGB(0, 229, 153) // Surfbot Signal Green #00E599
	errColor := color.New(color.FgRed)
	warn := color.New(color.FgYellow)
	muted := color.New(color.Faint)
	bold := color.New(color.Bold)

	newAssetsTotal := sumIntMap(delta.NewAssets)
	disAssetsTotal := sumIntMap(delta.DisappearedAssets)
	modAssetsTotal := sumIntMap(delta.ModifiedAssets)
	newFindingsTotal := sumSevMap(delta.NewFindings)
	resolvedFindingsTotal := sumSevMap(delta.ResolvedFindings)

	if delta.IsBaseline {
		muted.Fprintf(os.Stderr, "\nBASELINE SCAN — run again to detect changes.\n")
		return
	}

	if newAssetsTotal+disAssetsTotal+modAssetsTotal+newFindingsTotal+resolvedFindingsTotal == 0 {
		muted.Fprintf(os.Stderr, "\nNo changes since last scan.\n")
		return
	}

	fmt.Fprintln(os.Stderr, "")
	bold.Fprintln(os.Stderr, "CHANGES SINCE LAST SCAN")

	if newAssetsTotal > 0 {
		success.Fprintf(os.Stderr, "  + %d new %s%s\n",
			newAssetsTotal, pluralize("asset", newAssetsTotal), formatTypeBreakdown(delta.NewAssets))
	}
	if disAssetsTotal > 0 {
		errColor.Fprintf(os.Stderr, "  - %d disappeared %s%s\n",
			disAssetsTotal, pluralize("asset", disAssetsTotal), formatTypeBreakdown(delta.DisappearedAssets))
	}
	if modAssetsTotal > 0 {
		warn.Fprintf(os.Stderr, "  ~ %d modified %s%s\n",
			modAssetsTotal, pluralize("asset", modAssetsTotal), formatTypeBreakdown(delta.ModifiedAssets))
	}
	if newFindingsTotal > 0 {
		success.Fprintf(os.Stderr, "  + %d new %s%s\n",
			newFindingsTotal, pluralize("finding", newFindingsTotal), formatSevBreakdown(delta.NewFindings))
	}
	if resolvedFindingsTotal > 0 {
		successMuted := color.RGB(0, 229, 153).Add(color.Faint) // Signal Green + dim
		successMuted.Fprintf(os.Stderr, "  ✓ %d resolved %s\n",
			resolvedFindingsTotal, pluralize("finding", resolvedFindingsTotal))
	}
}

func sumIntMap(m map[model.AssetType]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

func sumSevMap(m map[model.Severity]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

// formatTypeBreakdown renders " (2 subdomains, 1 url)" etc. Returns "" when
// the map is empty or has exactly one key (the total is already shown).
func formatTypeBreakdown(m map[model.AssetType]int) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for typ, n := range m {
		parts = append(parts, fmt.Sprintf("%d %s", n, pluralize(string(typ), n)))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// formatSevBreakdown renders " (1 critical, 3 high)" ordered by severity.
func formatSevBreakdown(m map[model.Severity]int) string {
	if len(m) == 0 {
		return ""
	}
	order := []model.Severity{model.SeverityCritical, model.SeverityHigh, model.SeverityMedium, model.SeverityLow, model.SeverityInfo}
	parts := make([]string, 0, len(m))
	for _, sev := range order {
		if n := m[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
