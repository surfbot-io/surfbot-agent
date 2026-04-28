-- Issue #52: structured scan logs. Captures pipeline events
-- (scan/phase/tool lifecycle, findings emitted, asset changes) so the
-- webui can offer CLI-parity live log streaming and post-mortem
-- inspection. Logs are best-effort — findings + tool_runs remain the
-- canonical record. Pipeline writes are async + batched via the
-- SQLiteLogSink so persistence latency never gates a scan.
--
-- Schema decisions:
--   - id INTEGER AUTOINCREMENT: monotonic cursor for ?since=N pagination
--     (faster than RFC3339 timestamps, no clock-skew weirdness).
--   - tool_run_id NULLABLE: scan-level events (started/completed/error)
--     belong to the scan but no specific tool.
--   - level CHECK constraint: only debug/info/warn/error are valid.
--   - created_at separate from ts because retention queries scan by row
--     creation time, not by event time (which can be backfilled).
--   - FK CASCADE on scans: deleting a scan reaps its logs cleanly.
--   - tool_run_id intentionally has NO FK reference. The pipeline emits
--     ToolStarted log lines BEFORE the matching tool_runs row is
--     persisted (the start log fires at tool.Run() entry; the row is
--     INSERTed after the run completes). An FK with deferred enforcement
--     would close the gap, but SQLite only deferred-checks at COMMIT and
--     the sink batches across multiple scan/tool boundaries — easier to
--     drop the constraint than to thread DEFERRABLE through every code
--     path. Dangling tool_run_id pointers are harmless: the column is
--     opaque text used only for client-side grouping in the UI.

CREATE TABLE IF NOT EXISTS scan_logs (
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

-- Hot path: paginate logs of a single scan by id cursor.
CREATE INDEX IF NOT EXISTS idx_scan_logs_scan_id_id
    ON scan_logs(scan_id, id);

-- Retention sweeper: prune by row creation time.
CREATE INDEX IF NOT EXISTS idx_scan_logs_created_at
    ON scan_logs(created_at);

PRAGMA user_version = 6;

UPDATE agent_meta SET value = '6' WHERE key = 'schema_version';
