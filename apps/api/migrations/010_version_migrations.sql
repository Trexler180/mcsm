-- +goose Up
-- One row per server version-migration attempt. detail is a JSON progress
-- document the engine rewrites as it moves through phases (backup → reinstall →
-- move mods → verify → restore-on-failure), so the UI can poll a running row for
-- live progress. backup_id points at the safety-net backup taken before any
-- change, which the engine restores if the post-migration boot is unhealthy.
CREATE TABLE server_version_migrations (
    id              TEXT PRIMARY KEY,
    server_id       TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    from_mc_version TEXT NOT NULL DEFAULT '',
    to_mc_version   TEXT NOT NULL DEFAULT '',
    backup_id       TEXT,
    status          TEXT NOT NULL DEFAULT 'running',
    detail          TEXT,
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at     DATETIME
);

CREATE INDEX server_version_migrations_server
    ON server_version_migrations(server_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS server_version_migrations;
