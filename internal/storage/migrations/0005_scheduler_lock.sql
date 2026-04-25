-- SPEC-SCHED2.0 R5: scheduler_lock is the singleton-guard mechanism
-- that prevents two surfbot processes from running the scheduler
-- against the same DB at the same time. It holds at most one row
-- (enforced by the PK=1 check) identifying the process that currently
-- owns the dispatch loop. Second would-be acquirers read this row and
-- either fall back to UI-only mode or exit cleanly, depending on the
-- entry point.

CREATE TABLE IF NOT EXISTS scheduler_lock (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    pid           INTEGER NOT NULL,
    hostname      TEXT NOT NULL,
    acquired_at   TEXT NOT NULL,
    heartbeat_at  TEXT NOT NULL
);

PRAGMA user_version = 5;

UPDATE agent_meta SET value = '5' WHERE key = 'schema_version';
