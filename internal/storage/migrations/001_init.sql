-- Surfbot Agent local storage schema
-- Aligned with surfbot-api PostgreSQL schema for future sync (L4)

CREATE TABLE IF NOT EXISTS targets (
    id          TEXT PRIMARY KEY,
    value       TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL CHECK (type IN ('domain', 'cidr', 'ip')),
    scope       TEXT NOT NULL DEFAULT 'external' CHECK (scope IN ('external', 'internal', 'both')),
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_scan_id TEXT,
    last_scan_at TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assets (
    id          TEXT PRIMARY KEY,
    target_id   TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    parent_id   TEXT REFERENCES assets(id) ON DELETE SET NULL,
    type        TEXT NOT NULL CHECK (type IN ('domain', 'subdomain', 'ipv4', 'ipv6', 'port_service', 'url', 'technology', 'service')),
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

CREATE TABLE IF NOT EXISTS scans (
    id          TEXT PRIMARY KEY,
    target_id   TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    type        TEXT NOT NULL CHECK (type IN ('full', 'quick', 'discovery')),
    status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
    phase       TEXT NOT NULL DEFAULT '',
    progress    REAL NOT NULL DEFAULT 0,
    stats       TEXT NOT NULL DEFAULT '{}',
    started_at  TEXT,
    finished_at TEXT,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS findings (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    scan_id       TEXT REFERENCES scans(id) ON DELETE SET NULL,
    template_id   TEXT NOT NULL,
    template_name TEXT NOT NULL DEFAULT '',
    severity      TEXT NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    "references"  TEXT NOT NULL DEFAULT '[]',
    remediation   TEXT NOT NULL DEFAULT '',
    evidence      TEXT NOT NULL DEFAULT '',
    cvss          REAL,
    cve           TEXT,
    status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'resolved', 'false_positive', 'ignored')),
    source_tool   TEXT NOT NULL DEFAULT 'nuclei',
    confidence    REAL NOT NULL DEFAULT 50.0,
    first_seen    TEXT NOT NULL,
    last_seen     TEXT NOT NULL,
    resolved_at   TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE(asset_id, template_id, source_tool)
);

CREATE TABLE IF NOT EXISTS tool_runs (
    id              TEXT PRIMARY KEY,
    scan_id         TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    tool_name       TEXT NOT NULL,
    phase           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed', 'skipped', 'timeout')),
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    targets_count   INTEGER NOT NULL DEFAULT 0,
    findings_count  INTEGER NOT NULL DEFAULT 0,
    output_summary  TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER NOT NULL DEFAULT 0,
    config          TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS remediations (
    id          TEXT PRIMARY KEY,
    finding_id  TEXT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    tool_name   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'approved', 'running', 'completed', 'failed', 'rolled_back')),
    plan        TEXT NOT NULL DEFAULT '{}',
    result      TEXT NOT NULL DEFAULT '{}',
    dry_run     INTEGER NOT NULL DEFAULT 0,
    applied_at  TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Seed agent metadata
INSERT OR IGNORE INTO agent_meta (key, value) VALUES ('schema_version', '1');
INSERT OR IGNORE INTO agent_meta (key, value) VALUES ('agent_id', '');
INSERT OR IGNORE INTO agent_meta (key, value) VALUES ('cloud_synced', 'false');

-- Indexes
CREATE INDEX IF NOT EXISTS idx_assets_target_id ON assets(target_id);
CREATE INDEX IF NOT EXISTS idx_assets_type ON assets(type);
CREATE INDEX IF NOT EXISTS idx_assets_status ON assets(status);
CREATE INDEX IF NOT EXISTS idx_assets_parent_id ON assets(parent_id);
CREATE INDEX IF NOT EXISTS idx_findings_asset_id ON findings(asset_id);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);
CREATE INDEX IF NOT EXISTS idx_findings_status ON findings(status);
CREATE INDEX IF NOT EXISTS idx_findings_source_tool ON findings(source_tool);
CREATE INDEX IF NOT EXISTS idx_findings_scan_id ON findings(scan_id);
CREATE INDEX IF NOT EXISTS idx_scans_target_id ON scans(target_id);
CREATE INDEX IF NOT EXISTS idx_scans_status ON scans(status);
CREATE INDEX IF NOT EXISTS idx_tool_runs_scan_id ON tool_runs(scan_id);
CREATE INDEX IF NOT EXISTS idx_tool_runs_tool_name ON tool_runs(tool_name);
