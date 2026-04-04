package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"

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

// ChangeSummary holds aggregated change counts for display.
type ChangeSummary struct {
	NewAssets           int
	DisappearedAssets   int
	ModifiedAssets      int
	NewFindings         int
	ResolvedFindings    int
	IsBaseline          bool
	TotalBaselineAssets int
	NewByType           map[string]int
	CriticalModified    int
}

// BuildChangeSummary aggregates changes for display.
func BuildChangeSummary(changes []model.AssetChange, newFindings, resolvedFindings int) ChangeSummary {
	s := ChangeSummary{
		NewFindings:      newFindings,
		ResolvedFindings: resolvedFindings,
		NewByType:        make(map[string]int),
	}

	for _, c := range changes {
		if c.Baseline {
			s.IsBaseline = true
			s.TotalBaselineAssets++
			continue
		}
		switch c.ChangeType {
		case model.ChangeTypeAppeared:
			s.NewAssets++
			s.NewByType[c.AssetType]++
		case model.ChangeTypeDisappeared:
			s.DisappearedAssets++
		case model.ChangeTypeModified:
			s.ModifiedAssets++
			if c.Significance == model.SignificanceCritical {
				s.CriticalModified++
			}
		}
	}
	return s
}

// PrintChangeSummary prints the change summary to stderr.
func PrintChangeSummary(summary ChangeSummary) {
	if summary.IsBaseline {
		fmt.Fprintf(os.Stderr, "\nBASELINE SCAN — %d assets discovered. Run again to detect changes.\n", summary.TotalBaselineAssets)
		return
	}

	total := summary.NewAssets + summary.DisappearedAssets + summary.ModifiedAssets + summary.NewFindings + summary.ResolvedFindings
	if total == 0 {
		fmt.Fprintf(os.Stderr, "\nNo changes since last scan.\n")
		return
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "CHANGES SINCE LAST SCAN")

	if summary.NewAssets > 0 {
		parts := []string{}
		for typ, count := range summary.NewByType {
			parts = append(parts, fmt.Sprintf("%d %s", count, pluralize(typ, count)))
		}
		detail := ""
		if len(parts) > 0 {
			detail = " (" + strings.Join(parts, ", ") + ")"
		}
		fmt.Fprintf(os.Stderr, "  + %d new %s%s\n", summary.NewAssets, pluralize("asset", summary.NewAssets), detail)
	}
	if summary.DisappearedAssets > 0 {
		fmt.Fprintf(os.Stderr, "  - %d disappeared %s\n", summary.DisappearedAssets, pluralize("asset", summary.DisappearedAssets))
	}
	if summary.ModifiedAssets > 0 {
		detail := ""
		if summary.CriticalModified > 0 {
			detail = fmt.Sprintf(" (%d critical)", summary.CriticalModified)
		}
		fmt.Fprintf(os.Stderr, "  ~ %d modified %s%s\n", summary.ModifiedAssets, pluralize("asset", summary.ModifiedAssets), detail)
	}
	if summary.NewFindings > 0 {
		fmt.Fprintf(os.Stderr, "  + %d new %s\n", summary.NewFindings, pluralize("finding", summary.NewFindings))
	}
	if summary.ResolvedFindings > 0 {
		fmt.Fprintf(os.Stderr, "  ✓ %d resolved %s\n", summary.ResolvedFindings, pluralize("finding", summary.ResolvedFindings))
	}
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
