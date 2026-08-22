CREATE TABLE IF NOT EXISTS environment_baselines (
    id TEXT PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME,
    environment_id TEXT NOT NULL, name TEXT NOT NULL, description TEXT,
    created_by TEXT, container_configs TEXT, captured_at DATETIME NOT NULL,
    container_count INTEGER NOT NULL DEFAULT 0, is_active BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_environment_baselines_environment_id ON environment_baselines(environment_id);
CREATE TABLE IF NOT EXISTS drift_records (
    id TEXT PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME,
    baseline_id TEXT NOT NULL, environment_id TEXT NOT NULL, container_name TEXT,
    container_id TEXT, drift_type TEXT NOT NULL, field TEXT, expected_value TEXT,
    actual_value TEXT, severity TEXT NOT NULL, status TEXT NOT NULL,
    detected_at DATETIME NOT NULL, resolved_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_drift_records_baseline_id ON drift_records(baseline_id);
CREATE TABLE IF NOT EXISTS compliance_snapshots (
    id TEXT PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME,
    environment_id TEXT NOT NULL, baseline_id TEXT NOT NULL,
    total_containers INTEGER NOT NULL DEFAULT 0, compliant_containers INTEGER NOT NULL DEFAULT 0,
    drifted_containers INTEGER NOT NULL DEFAULT 0, missing_containers INTEGER NOT NULL DEFAULT 0,
    added_containers INTEGER NOT NULL DEFAULT 0, critical_drifts INTEGER NOT NULL DEFAULT 0,
    high_drifts INTEGER NOT NULL DEFAULT 0, medium_drifts INTEGER NOT NULL DEFAULT 0,
    low_drifts INTEGER NOT NULL DEFAULT 0, compliance_score REAL NOT NULL DEFAULT 100
);
