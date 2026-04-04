CREATE TABLE IF NOT EXISTS asset_changes (
    id             TEXT PRIMARY KEY,
    target_id      TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    scan_id        TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    asset_id       TEXT REFERENCES assets(id) ON DELETE SET NULL,
    change_type    TEXT NOT NULL CHECK (change_type IN ('appeared', 'disappeared', 'modified')),
    significance   TEXT NOT NULL CHECK (significance IN ('critical', 'high', 'medium', 'low', 'info', 'noise')),
    asset_type     TEXT NOT NULL,
    asset_value    TEXT NOT NULL,
    previous_meta  TEXT NOT NULL DEFAULT '{}',
    current_meta   TEXT NOT NULL DEFAULT '{}',
    summary        TEXT NOT NULL,
    baseline       INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_asset_changes_target ON asset_changes(target_id);
CREATE INDEX IF NOT EXISTS idx_asset_changes_scan ON asset_changes(scan_id);
CREATE INDEX IF NOT EXISTS idx_asset_changes_significance ON asset_changes(significance);
CREATE INDEX IF NOT EXISTS idx_asset_changes_type ON asset_changes(change_type);
