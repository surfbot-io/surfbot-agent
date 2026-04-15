-- Migration 003: scan_state_model
--
-- Replaces the legacy scans.stats JSON blob with three semantically-distinct
-- snapshots: target_state, delta, work. See SPEC-QA3 (SUR-244).
--
-- Also:
--  * Opens up assets.type (drops CHECK constraint) so that new detection
--    tools can introduce new AssetTypes without requiring a SQL migration.
--    Validation lives in Go — the database treats the column as opaque.
--  * Adds findings.first_seen_scan_id to preserve the original discovery
--    scan when findings.scan_id is later overwritten by re-observation
--    upserts.
--
-- Pre-launch destructive migration: legacy stats data in scans.stats is
-- dropped. New target_state/delta/work snapshots are zero-valued for
-- pre-existing scans; they will be correctly populated on the next run.

-- === scans: replace stats with three snapshots ==============================

ALTER TABLE scans ADD COLUMN target_state TEXT NOT NULL DEFAULT '{}';
ALTER TABLE scans ADD COLUMN delta        TEXT NOT NULL DEFAULT '{}';
ALTER TABLE scans ADD COLUMN work         TEXT NOT NULL DEFAULT '{}';

-- Drop the legacy blob. SQLite 3.35+ supports DROP COLUMN; modernc.org/sqlite
-- embeds a recent enough build. If that assumption ever breaks, convert this
-- to a CREATE TABLE … AS + swap.
ALTER TABLE scans DROP COLUMN stats;

-- === findings: preserve first-discovery scan ================================

ALTER TABLE findings ADD COLUMN first_seen_scan_id TEXT REFERENCES scans(id) ON DELETE SET NULL;

-- Backfill: existing rows' scan_id was assigned on first insert (UpsertFinding
-- didn't update it on conflict), so it already represents first-discovery.
-- Copy it into first_seen_scan_id before scan_id starts tracking "latest".
UPDATE findings SET first_seen_scan_id = scan_id WHERE first_seen_scan_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_findings_first_seen_scan_id ON findings(first_seen_scan_id);

-- === assets: drop CHECK on type =============================================
--
-- SQLite cannot drop a CHECK constraint in place, so we rebuild the table.
-- The legacy schema pinned the type column to a closed vocabulary; new
-- detection tools would have required a migration to emit a new AssetType.
-- After this change the database is agnostic to the AssetType vocabulary.

CREATE TABLE assets_new (
    id          TEXT PRIMARY KEY,
    target_id   TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    parent_id   TEXT REFERENCES assets(id) ON DELETE SET NULL,
    type        TEXT NOT NULL,
    value       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'new', 'disappeared', 'returned', 'inactive', 'ignored')),
    tags        TEXT NOT NULL DEFAULT '[]',
    metadata    TEXT NOT NULL DEFAULT '{}',
    first_seen  TEXT NOT NULL,
    last_seen   TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(target_id, value)
);

INSERT INTO assets_new (id, target_id, parent_id, type, value, status, tags, metadata,
                        first_seen, last_seen, created_at, updated_at)
SELECT id, target_id, parent_id, type, value, status, tags, metadata,
       first_seen, last_seen, created_at, updated_at
FROM assets;

DROP TABLE assets;
ALTER TABLE assets_new RENAME TO assets;

CREATE INDEX IF NOT EXISTS idx_assets_target_id ON assets(target_id);
CREATE INDEX IF NOT EXISTS idx_assets_type      ON assets(type);
CREATE INDEX IF NOT EXISTS idx_assets_status    ON assets(status);
CREATE INDEX IF NOT EXISTS idx_assets_parent_id ON assets(parent_id);

-- === schema version =========================================================

UPDATE agent_meta SET value = '3' WHERE key = 'schema_version';
