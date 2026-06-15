-- +goose Up
-- Versions the auto-updater must never install again: when an update made the
-- server crash on boot, the update is reverted and the offending version lands
-- here so the next run picks the newest version NOT in this table.
CREATE TABLE mod_skipped_versions (
    server_id  TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    mod_name   TEXT NOT NULL DEFAULT '',
    version    TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, project_id, version_id)
);

-- One row per auto-update attempt (manual or scheduled). detail is a JSON
-- progress document the engine rewrites as it moves through phases, so the UI
-- can poll a running row and show live progress.
CREATE TABLE mod_update_runs (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    trigger     TEXT NOT NULL DEFAULT 'manual',
    status      TEXT NOT NULL DEFAULT 'running',
    detail      TEXT,
    started_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE INDEX mod_update_runs_server ON mod_update_runs(server_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS mod_update_runs;
DROP TABLE IF EXISTS mod_skipped_versions;
