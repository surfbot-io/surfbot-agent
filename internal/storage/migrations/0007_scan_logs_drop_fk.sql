-- Issue #52 follow-up: the original 0006 (shipped briefly in PR #53,
-- reverted in #54) declared `tool_run_id REFERENCES tool_runs(id) ON
-- DELETE SET NULL`. The FK rejected inserts because the pipeline emits
-- ToolStarted log lines BEFORE the tool_runs row is persisted, so the
-- entire batch failed under foreign_keys=1.
--
-- This migration recreates `scan_logs` without the tool_run_id FK on
-- any DB that's already at user_version=6 from the buggy 0006. SQLite
-- doesn't support DROP CONSTRAINT, so we use the standard rebuild
-- pattern: new table → copy rows → drop old → rename. The new schema
-- matches the (also-fixed) 0006 verbatim.
--
-- Idempotent against a fresh DB that ran the corrected 0006: the
-- rebuild produces an identical schema; the existing rows (probably
-- zero on a fresh install) round-trip unchanged.

CREATE TABLE IF NOT EXISTS scan_logs_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_id      TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    tool_run_id  TEXT,
    ts           INTEGER NOT NULL,
    source       TEXT NOT NULL,
    level        TEXT NOT NULL DEFAULT 'info'
                 CHECK (level IN ('debug','info','warn','error')),
    text         TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

INSERT INTO scan_logs_new (id, scan_id, tool_run_id, ts, source, level, text, created_at)
SELECT id, scan_id, tool_run_id, ts, source, level, text, created_at
FROM scan_logs;

DROP TABLE scan_logs;
ALTER TABLE scan_logs_new RENAME TO scan_logs;

CREATE INDEX IF NOT EXISTS idx_scan_logs_scan_id_id
    ON scan_logs(scan_id, id);

CREATE INDEX IF NOT EXISTS idx_scan_logs_created_at
    ON scan_logs(created_at);

PRAGMA user_version = 7;

UPDATE agent_meta SET value = '7' WHERE key = 'schema_version';
