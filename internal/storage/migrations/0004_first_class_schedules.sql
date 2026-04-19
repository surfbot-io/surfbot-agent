-- Migration 0004: first-class schedules (SPEC-SCHED1.1)
--
-- Promotes schedules from a singleton schedule.config.json to first-class
-- resources. Creates five new tables: scan_schedules, scan_templates,
-- blackout_windows, schedule_defaults, ad_hoc_scan_runs.
--
-- This migration is purely additive — existing scans, targets, assets, and
-- findings tables are untouched. The new tables sit dormant until PR
-- SCHED1.2 wires the master ticker to them.

CREATE TABLE IF NOT EXISTS scan_templates (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    rrule              TEXT NOT NULL,
    timezone           TEXT NOT NULL DEFAULT 'UTC',
    tool_config        TEXT NOT NULL DEFAULT '{}',
    maintenance_window TEXT,
    is_system          INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scan_templates_updated ON scan_templates(updated_at);

CREATE TABLE IF NOT EXISTS scan_schedules (
    id                 TEXT PRIMARY KEY,
    target_id          TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    rrule              TEXT NOT NULL,
    dtstart            TEXT NOT NULL,
    timezone           TEXT NOT NULL,
    template_id        TEXT REFERENCES scan_templates(id) ON DELETE SET NULL,
    tool_config        TEXT NOT NULL DEFAULT '{}',
    overrides          TEXT NOT NULL DEFAULT '[]',
    maintenance_window TEXT,
    enabled            INTEGER NOT NULL DEFAULT 1,
    next_run_at        TEXT,
    last_run_at        TEXT,
    last_run_status    TEXT,
    last_scan_id       TEXT REFERENCES scans(id) ON DELETE SET NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE(target_id, name)
);

CREATE INDEX IF NOT EXISTS idx_scan_schedules_next_run ON scan_schedules(next_run_at) WHERE enabled = 1;
CREATE INDEX IF NOT EXISTS idx_scan_schedules_target ON scan_schedules(target_id);
CREATE INDEX IF NOT EXISTS idx_scan_schedules_template ON scan_schedules(template_id);

CREATE TABLE IF NOT EXISTS blackout_windows (
    id            TEXT PRIMARY KEY,
    scope         TEXT NOT NULL CHECK (scope IN ('global', 'target')),
    target_id     TEXT REFERENCES targets(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    rrule         TEXT NOT NULL,
    duration_sec  INTEGER NOT NULL,
    timezone      TEXT NOT NULL DEFAULT 'UTC',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    CHECK ((scope = 'global' AND target_id IS NULL) OR (scope = 'target' AND target_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_blackout_scope ON blackout_windows(scope, enabled);
CREATE INDEX IF NOT EXISTS idx_blackout_target ON blackout_windows(target_id) WHERE target_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS schedule_defaults (
    id                          INTEGER PRIMARY KEY CHECK (id = 1),
    default_template_id         TEXT REFERENCES scan_templates(id) ON DELETE SET NULL,
    default_rrule               TEXT NOT NULL,
    default_timezone            TEXT NOT NULL DEFAULT 'UTC',
    default_tool_config         TEXT NOT NULL DEFAULT '{}',
    default_maintenance_window  TEXT,
    max_concurrent_scans        INTEGER NOT NULL DEFAULT 4,
    run_on_start                INTEGER NOT NULL DEFAULT 0,
    jitter_seconds              INTEGER NOT NULL DEFAULT 60,
    updated_at                  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ad_hoc_scan_runs (
    id               TEXT PRIMARY KEY,
    target_id        TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    template_id      TEXT REFERENCES scan_templates(id) ON DELETE SET NULL,
    tool_config      TEXT NOT NULL DEFAULT '{}',
    initiated_by     TEXT NOT NULL,
    reason           TEXT NOT NULL DEFAULT '',
    scan_id          TEXT REFERENCES scans(id) ON DELETE SET NULL,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    requested_at     TEXT NOT NULL,
    started_at       TEXT,
    completed_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_adhoc_target ON ad_hoc_scan_runs(target_id, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_adhoc_status ON ad_hoc_scan_runs(status);

-- Seed the singleton schedule_defaults row. Values here are placeholders;
-- the legacy migration function (PR SCHED1.2) overwrites them with data
-- derived from schedule.config.json on first boot post-upgrade.
INSERT OR IGNORE INTO schedule_defaults (
    id, default_rrule, default_timezone, default_tool_config,
    max_concurrent_scans, run_on_start, jitter_seconds, updated_at
) VALUES (
    1, 'FREQ=DAILY;BYHOUR=2', 'UTC', '{}', 4, 0, 60, CURRENT_TIMESTAMP
);

PRAGMA user_version = 4;

UPDATE agent_meta SET value = '4' WHERE key = 'schema_version';
