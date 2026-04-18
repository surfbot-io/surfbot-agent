package storage

// Aggregate queries used by the pipeline's finalize* functions to compute
// TargetState / ScanDelta / ScanWork at the end of a scan. All counts come
// from the database so that the persisted aggregates match what `list`
// subcommands return — no in-memory accumulator drift.
//
// Conventions:
//   * Target-scoped: aggregates that answer "what does the target look like?"
//     (assets, findings by status). Filter by target_id, not scan_id.
//   * Scan-scoped: aggregates that answer "what did this scan do/change?"
//     (asset_changes, tool_runs). Filter by scan_id.
//
// Empty results return an empty map and a nil error, never an error.

import (
	"context"
	"fmt"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// CountAssetsByTypeForTarget returns a map[AssetType]count of assets for the
// given target, filtered to the "live" statuses (active|new|returned).
// Disappeared/inactive/ignored assets are excluded — this answers "what
// currently exists", not "what has ever existed."
func (s *SQLiteStore) CountAssetsByTypeForTarget(ctx context.Context, targetID string) (map[model.AssetType]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT type, COUNT(*) FROM assets
		 WHERE target_id = ? AND status IN ('active', 'new', 'returned')
		 GROUP BY type`,
		targetID)
	if err != nil {
		return nil, fmt.Errorf("counting assets by type for target %s: %w", targetID, err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	counts := make(map[model.AssetType]int)
	for rows.Next() {
		var typ model.AssetType
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, fmt.Errorf("scanning asset type count: %w", err)
		}
		counts[typ] = n
	}
	return counts, rows.Err()
}

// CountPortsByStatusForTarget returns a map[status]count of port_service
// assets for the target, bucketed by metadata.status. Non-port_service
// assets are excluded. Returns an empty map (not nil) when the target has
// no port assets.
//
// Port status vocabulary is open — whatever string a port_scan tool writes
// into metadata.status becomes a key. Ports with no status metadata bucket
// under "unknown" so the sum equals the port_service asset count.
func (s *SQLiteStore) CountPortsByStatusForTarget(ctx context.Context, targetID string) (map[string]int, error) {
	// Alias explicitly — bare "status" collides with the column name and
	// SQLite would group by the asset_status instead of the JSON status.
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(NULLIF(json_extract(metadata, '$.status'), ''), 'unknown') AS port_status,
		        COUNT(*)
		 FROM assets
		 WHERE target_id = ? AND type = ? AND status IN ('active', 'new', 'returned')
		 GROUP BY port_status`,
		targetID, string(model.AssetTypePort))
	if err != nil {
		return nil, fmt.Errorf("counting ports by status for target %s: %w", targetID, err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scanning port status count: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// CountFindingsBySeverityForTarget returns a map[Severity]count of findings
// for assets belonging to the target, optionally filtered by status. Pass
// an empty status to count all findings regardless of status.
func (s *SQLiteStore) CountFindingsBySeverityForTarget(ctx context.Context, targetID string, status model.FindingStatus) (map[model.Severity]int, error) {
	query := `SELECT severity, COUNT(*) FROM findings
	          WHERE asset_id IN (SELECT id FROM assets WHERE target_id = ?)`
	args := []interface{}{targetID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, string(status))
	}
	query += ` GROUP BY severity`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting findings by severity for target %s: %w", targetID, err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	counts := make(map[model.Severity]int)
	for rows.Next() {
		var sev model.Severity
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, fmt.Errorf("scanning finding severity count: %w", err)
		}
		counts[sev] = n
	}
	return counts, rows.Err()
}

// CountFindingsByStatusForTarget returns a map[FindingStatus]count of
// findings for assets belonging to the target.
func (s *SQLiteStore) CountFindingsByStatusForTarget(ctx context.Context, targetID string) (map[model.FindingStatus]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM findings
		 WHERE asset_id IN (SELECT id FROM assets WHERE target_id = ?)
		 GROUP BY status`,
		targetID)
	if err != nil {
		return nil, fmt.Errorf("counting findings by status for target %s: %w", targetID, err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	counts := make(map[model.FindingStatus]int)
	for rows.Next() {
		var st model.FindingStatus
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("scanning finding status count: %w", err)
		}
		counts[st] = n
	}
	return counts, rows.Err()
}

// AssetChangeCountsForScan returns the change counts for a scan, keyed by
// (change_type, asset_type). The outer map's keys are change_type strings
// ("appeared", "disappeared", "modified"); the inner map buckets by the
// asset type that changed. Used to build ScanDelta.
func (s *SQLiteStore) AssetChangeCountsForScan(ctx context.Context, scanID string) (map[string]map[model.AssetType]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT change_type, asset_type, COUNT(*)
		 FROM asset_changes
		 WHERE scan_id = ? AND baseline = 0
		 GROUP BY change_type, asset_type`,
		scanID)
	if err != nil {
		return nil, fmt.Errorf("counting asset changes for scan %s: %w", scanID, err)
	}
	defer rows.Close() //nolint:errcheck // close errors on a deferred cursor are not actionable

	counts := make(map[string]map[model.AssetType]int)
	for rows.Next() {
		var changeType, assetType string
		var n int
		if err := rows.Scan(&changeType, &assetType, &n); err != nil {
			return nil, fmt.Errorf("scanning asset change count: %w", err)
		}
		if _, ok := counts[changeType]; !ok {
			counts[changeType] = make(map[model.AssetType]int)
		}
		counts[changeType][model.AssetType(assetType)] = n
	}
	return counts, rows.Err()
}

// ScanIsBaseline reports whether any asset_change for the scan carries the
// baseline flag. Used by ScanDelta.IsBaseline.
func (s *SQLiteStore) ScanIsBaseline(ctx context.Context, scanID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM asset_changes WHERE scan_id = ? AND baseline = 1 LIMIT 1`,
		scanID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("checking baseline for scan %s: %w", scanID, err)
	}
	return n > 0, nil
}
