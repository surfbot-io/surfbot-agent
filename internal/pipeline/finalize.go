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

	// Track the earliest started_at per phase so we can emit PhasesRun in
	// actual execution order, not alphabetic. Alphabetic sort would
	// suggest to an LLM consumer that nuclei ran before subfinder, which
	// is obviously wrong. Phases with no timestamp fall back to the
	// iteration order of the runs slice (which is DB insertion order).
	phaseFirstSeen := make(map[string]time.Time)
	phaseInsertOrder := make(map[string]int)
	insertOrder := 0
	for _, r := range runs {
		work.ToolsRun++
		switch r.Status {
		case model.ToolRunFailed, model.ToolRunTimeout:
			work.ToolsFailed++
		case model.ToolRunSkipped:
			work.ToolsSkipped++
		}
		work.RawEmissions += r.FindingsCount
		if r.Phase == "" {
			continue
		}
		existing, seen := phaseFirstSeen[r.Phase]
		if !seen {
			phaseFirstSeen[r.Phase] = r.StartedAt
			phaseInsertOrder[r.Phase] = insertOrder
			insertOrder++
			continue
		}
		// Keep the earliest timestamp for a phase in case multiple tools
		// share a phase (future-proofing: today each phase has one tool,
		// but the model doesn't enforce that).
		if !r.StartedAt.IsZero() && (existing.IsZero() || r.StartedAt.Before(existing)) {
			phaseFirstSeen[r.Phase] = r.StartedAt
		}
	}

	work.PhasesRun = make([]string, 0, len(phaseFirstSeen))
	for p := range phaseFirstSeen {
		work.PhasesRun = append(work.PhasesRun, p)
	}
	sort.Slice(work.PhasesRun, func(i, j int) bool {
		pi, pj := work.PhasesRun[i], work.PhasesRun[j]
		a, b := phaseFirstSeen[pi], phaseFirstSeen[pj]
		// Zero-timestamp phases fall back to DB insert order (pipeline
		// order, since recordToolRun is called inline during the loop).
		switch {
		case a.IsZero() && b.IsZero():
			return phaseInsertOrder[pi] < phaseInsertOrder[pj]
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		case a.Equal(b):
			// Timestamps collide at second-level granularity in SQLite
			// (RFC3339 format). Without an explicit tiebreaker sort.Slice
			// is non-deterministic and the test hits a different order
			// under -race than without. Fall back to pipeline insert
			// order so the output is stable regardless of scheduling.
			return phaseInsertOrder[pi] < phaseInsertOrder[pj]
		default:
			return a.Before(b)
		}
	})

	return work, nil
}
