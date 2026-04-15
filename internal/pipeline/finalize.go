package pipeline

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// FinalizeTargetState queries the database to build a snapshot of the
// target's observed state at the moment the scan completed. Source of
// truth: assets, findings (all rows for the target); no in-memory counters.
//
// The returned TargetState is self-consistent with what `asset list` and
// `finding list` subcommands report — re-running those commands immediately
// after persistence yields the same counts.
func FinalizeTargetState(ctx context.Context, store storage.Store, targetID string) (model.TargetState, error) {
	state := model.TargetState{
		AssetsByType:     make(map[model.AssetType]int),
		FindingsOpen:     make(map[model.Severity]int),
		FindingsByStatus: make(map[model.FindingStatus]int),
		Remediations:     make(map[model.RemediationStatus]int),
	}

	byType, err := store.CountAssetsByTypeForTarget(ctx, targetID)
	if err != nil {
		return state, fmt.Errorf("target_state assets_by_type: %w", err)
	}
	state.AssetsByType = byType
	for _, n := range byType {
		state.AssetsTotal += n
	}

	// ports_by_status is only populated when the target actually has port
	// assets — keep the map nil / omitempty on the JSON side otherwise.
	if byType[model.AssetTypePort] > 0 {
		ports, err := store.CountPortsByStatusForTarget(ctx, targetID)
		if err != nil {
			return state, fmt.Errorf("target_state ports_by_status: %w", err)
		}
		state.PortsByStatus = ports
	}

	open, err := store.CountFindingsBySeverityForTarget(ctx, targetID, model.FindingStatusOpen)
	if err != nil {
		return state, fmt.Errorf("target_state findings_open: %w", err)
	}
	state.FindingsOpen = open
	for _, n := range open {
		state.FindingsOpenTotal += n
	}

	byStatus, err := store.CountFindingsByStatusForTarget(ctx, targetID)
	if err != nil {
		return state, fmt.Errorf("target_state findings_by_status: %w", err)
	}
	state.FindingsByStatus = byStatus

	// Remediations aggregate is an empty map for now — remediation storage
	// is not yet implemented. The field stays in the contract so future
	// wiring is purely additive.
	return state, nil
}

// FinalizeScanDelta derives ScanDelta from the asset_changes table and the
// already-computed finding change lists (newFindings, resolvedFindings are
// computed during the diff phase; pass them in so we don't re-run the diff).
//
// The returned ScanDelta reflects only what this scan actually changed —
// baseline scans are reported as IsBaseline=true with empty change buckets.
func FinalizeScanDelta(
	ctx context.Context,
	store storage.Store,
	scanID string,
	newFindings []model.Finding,
	resolvedFindings []model.Finding,
) (model.ScanDelta, error) {
	delta := model.ScanDelta{
		NewAssets:         make(map[model.AssetType]int),
		DisappearedAssets: make(map[model.AssetType]int),
		ModifiedAssets:    make(map[model.AssetType]int),
		NewFindings:       make(map[model.Severity]int),
		ResolvedFindings:  make(map[model.Severity]int),
		ReturnedFindings:  make(map[model.Severity]int),
	}

	isBaseline, err := store.ScanIsBaseline(ctx, scanID)
	if err != nil {
		return delta, fmt.Errorf("delta is_baseline: %w", err)
	}
	delta.IsBaseline = isBaseline

	// Baseline scans have every asset flagged appeared+baseline; we don't
	// bubble those into new_assets because they're not a true delta.
	if !isBaseline {
		changeCounts, err := store.AssetChangeCountsForScan(ctx, scanID)
		if err != nil {
			return delta, fmt.Errorf("delta asset_changes: %w", err)
		}
		if m := changeCounts[string(model.ChangeTypeAppeared)]; m != nil {
			delta.NewAssets = m
		}
		if m := changeCounts[string(model.ChangeTypeDisappeared)]; m != nil {
			delta.DisappearedAssets = m
		}
		if m := changeCounts[string(model.ChangeTypeModified)]; m != nil {
			delta.ModifiedAssets = m
		}
	}

	for _, f := range newFindings {
		delta.NewFindings[f.Severity]++
	}
	for _, f := range resolvedFindings {
		delta.ResolvedFindings[f.Severity]++
	}
	// ReturnedFindings ("resolved then re-opened") is not yet computed by
	// the diff phase — the detection of reopened findings would require
	// tracking status transitions over scans. Left empty for now; the key
	// stays in the contract so consumers can rely on a stable shape.

	return delta, nil
}

// FinalizeScanWork gathers the telemetry of the scan execution: total
// duration, tools run/failed/skipped, phases executed, and the raw
// pre-dedup emission count (sum of tool_runs.findings_count).
//
// Source: tool_runs rows with scan_id plus scan timing. Duration is passed
// in because the scan's FinishedAt hasn't been persisted yet at call time.
func FinalizeScanWork(
	ctx context.Context,
	store storage.Store,
	scanID string,
	duration time.Duration,
) (model.ScanWork, error) {
	work := model.ScanWork{
		DurationMs: duration.Milliseconds(),
	}

	runs, err := store.ListToolRuns(ctx, scanID)
	if err != nil {
		return work, fmt.Errorf("work tool_runs: %w", err)
	}

	phaseSet := make(map[string]struct{})
	for _, r := range runs {
		work.ToolsRun++
		switch r.Status {
		case model.ToolRunFailed, model.ToolRunTimeout:
			work.ToolsFailed++
		case model.ToolRunSkipped:
			work.ToolsSkipped++
		}
		work.RawEmissions += r.FindingsCount
		if r.Phase != "" {
			phaseSet[r.Phase] = struct{}{}
		}
	}

	work.PhasesRun = make([]string, 0, len(phaseSet))
	for p := range phaseSet {
		work.PhasesRun = append(work.PhasesRun, p)
	}
	sort.Strings(work.PhasesRun)

	return work, nil
}
