package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// TestMigration0004_FreshDB verifies that migration 0004 creates all five
// schedule tables and all seven indexes from the spec on a fresh database.
func TestMigration0004_FreshDB(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wantTables := []string{
		"scan_schedules",
		"scan_templates",
		"blackout_windows",
		"schedule_defaults",
		"ad_hoc_scan_runs",
	}
	for _, table := range wantTables {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		require.NoError(t, err, "table %s should exist", table)
		assert.Equal(t, table, name)
	}

	wantIndexes := []string{
		"idx_scan_schedules_next_run",
		"idx_scan_schedules_target",
		"idx_scan_schedules_template",
		"idx_scan_templates_updated",
		"idx_blackout_scope",
		"idx_blackout_target",
		"idx_adhoc_target",
		"idx_adhoc_status",
	}
	for _, idx := range wantIndexes {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		require.NoError(t, err, "index %s should exist", idx)
	}

	// The newTestStore helper applies all embedded migrations, so the
	// version reflects the latest (0005 adds scheduler_lock per
	// SPEC-SCHED2.0).
	v, err := s.GetMeta(ctx, "schema_version")
	require.NoError(t, err)
	assert.Equal(t, "5", v)

	var userVersion int
	err = s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion)
	require.NoError(t, err)
	assert.Equal(t, 5, userVersion)

	var defaultsCount int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedule_defaults`).Scan(&defaultsCount)
	require.NoError(t, err)
	assert.Equal(t, 1, defaultsCount, "schedule_defaults must seed exactly one row")
}

// TestMigration0004_WithExistingData verifies that migration 0004 does not
// touch populated scans/targets/assets/findings tables when it runs.
func TestMigration0004_WithExistingData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	scan := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusRunning}
	require.NoError(t, s.CreateScan(ctx, scan))

	assertRowCount := func(t *testing.T, table string, want int) {
		t.Helper()
		var n int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
		require.NoError(t, err)
		assert.Equal(t, want, n, "table %s row count", table)
	}

	assertRowCount(t, "targets", 1)
	assertRowCount(t, "scans", 1)
	assertRowCount(t, "scan_schedules", 0)
	assertRowCount(t, "scan_templates", 0)
	assertRowCount(t, "blackout_windows", 0)
	assertRowCount(t, "ad_hoc_scan_runs", 0)
	assertRowCount(t, "schedule_defaults", 1)
}

// TestMigration0004_ForwardOnly documents that no down migration exists —
// the repo policy is forward-only. This test pins the policy so a future
// change intending to add a down migration has to make it explicit.
func TestMigration0004_ForwardOnly(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), "_down_", "no down migrations allowed in %s", e.Name())
		assert.NotContains(t, e.Name(), ".down.", "no down migrations allowed in %s", e.Name())
	}
}

// TestMigration0004_ForeignKeys enforces the FK constraints declared in the
// migration: deleting a target cascades to its schedules, blackouts, and
// ad-hoc runs; deleting a template nulls template_id on schedules and
// ad-hoc runs.
func TestMigration0004_ForeignKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	now := time.Now().UTC().Format(time.RFC3339)
	tmplID := "tmpl-01"
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scan_templates (id, name, description, rrule, timezone, tool_config, is_system, created_at, updated_at)
		 VALUES (?, 'default', '', 'FREQ=DAILY', 'UTC', '{}', 1, ?, ?)`,
		tmplID, now, now)
	require.NoError(t, err)

	schedID := "sched-01"
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO scan_schedules (id, target_id, name, rrule, dtstart, timezone, template_id, created_at, updated_at)
		 VALUES (?, ?, 'default', 'FREQ=DAILY', ?, 'UTC', ?, ?, ?)`,
		schedID, target.ID, now, tmplID, now, now)
	require.NoError(t, err)

	blID := "bl-01"
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO blackout_windows (id, scope, target_id, name, rrule, duration_sec, timezone, created_at, updated_at)
		 VALUES (?, 'target', ?, 'night', 'FREQ=DAILY;BYHOUR=2', 3600, 'UTC', ?, ?)`,
		blID, target.ID, now, now)
	require.NoError(t, err)

	ahID := "ah-01"
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ad_hoc_scan_runs (id, target_id, template_id, initiated_by, status, requested_at)
		 VALUES (?, ?, ?, 'cli', 'pending', ?)`,
		ahID, target.ID, tmplID, now)
	require.NoError(t, err)

	// Delete the template: schedules and ad-hoc runs should have template_id NULL.
	_, err = s.db.ExecContext(ctx, `DELETE FROM scan_templates WHERE id = ?`, tmplID)
	require.NoError(t, err)

	var tmplRef sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT template_id FROM scan_schedules WHERE id = ?`, schedID).Scan(&tmplRef)
	require.NoError(t, err)
	assert.False(t, tmplRef.Valid, "template_id should be NULL after template delete")

	err = s.db.QueryRowContext(ctx,
		`SELECT template_id FROM ad_hoc_scan_runs WHERE id = ?`, ahID).Scan(&tmplRef)
	require.NoError(t, err)
	assert.False(t, tmplRef.Valid)

	// Delete the target: schedules, target-scoped blackouts, and ad-hoc runs cascade.
	_, err = s.db.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, target.ID)
	require.NoError(t, err)

	var n int
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scan_schedules WHERE id = ?`, schedID).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blackout_windows WHERE id = ?`, blID).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ad_hoc_scan_runs WHERE id = ?`, ahID).Scan(&n))
	assert.Equal(t, 0, n)
}

// TestMigration0004_Constraints exercises the CHECK and UNIQUE constraints.
func TestMigration0004_Constraints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)

	// schedule_defaults is a singleton.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedule_defaults (id, default_rrule, updated_at) VALUES (2, 'FREQ=DAILY', ?)`, now)
	assert.Error(t, err, "id != 1 must fail CHECK")

	// Global blackout with non-null target_id must fail.
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO blackout_windows (id, scope, target_id, name, rrule, duration_sec, timezone, created_at, updated_at)
		 VALUES ('x', 'global', 'some-target', 'bad', 'FREQ=DAILY', 60, 'UTC', ?, ?)`, now, now)
	assert.Error(t, err, "global scope with non-null target_id must fail CHECK")

	// Target blackout with null target_id must fail.
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO blackout_windows (id, scope, target_id, name, rrule, duration_sec, timezone, created_at, updated_at)
		 VALUES ('y', 'target', NULL, 'bad', 'FREQ=DAILY', 60, 'UTC', ?, ?)`, now, now)
	assert.Error(t, err, "target scope with null target_id must fail CHECK")

	// (target_id, name) uniqueness.
	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO scan_schedules (id, target_id, name, rrule, dtstart, timezone, created_at, updated_at)
		 VALUES ('s1', ?, 'default', 'FREQ=DAILY', ?, 'UTC', ?, ?)`, target.ID, now, now, now)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO scan_schedules (id, target_id, name, rrule, dtstart, timezone, created_at, updated_at)
		 VALUES ('s2', ?, 'default', 'FREQ=DAILY', ?, 'UTC', ?, ?)`, target.ID, now, now, now)
	assert.Error(t, err, "two schedules with same (target_id, name) must fail UNIQUE")
}

// TestMigration0004_PartialIndex verifies the partial next_run_at index is
// used for the hot ticker query.
func TestMigration0004_PartialIndex(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT id FROM scan_schedules WHERE enabled = 1 AND next_run_at <= ?`,
		time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var plan string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		plan += detail + "\n"
	}
	assert.Contains(t, plan, "idx_scan_schedules_next_run",
		"query must use partial next_run_at index")
}
