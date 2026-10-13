-- +goose Up

-- One row per long-running thing done to a server: a start (which may install
-- a runtime first), reinstall, backup, restore, version migration, safe mod
-- update, world upload, deletion. The dashboard follows progress here, and the
-- row outlives the request that began it, so closing the browser cancels
-- nothing and a restart of the API leaves an honest "interrupted" behind.
--
-- An exclusive operation holds the server: the partial unique index lets at
-- most one run at a time, so an update can no longer swap mods underneath a
-- migration, and a stop can no longer land in the middle of a boot check and
-- be mistaken for a broken update.
--
-- Times are unix seconds, which compare without date-format quirks.
CREATE TABLE operations (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'running', -- running|succeeded|failed|cancelled|interrupted
    exclusive      INTEGER NOT NULL DEFAULT 1,
    trigger        TEXT NOT NULL DEFAULT 'manual',  -- manual|scheduled|automatic|agent
    actor_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor_key_id   TEXT,
    actor_grant_id TEXT,
    phase          TEXT NOT NULL DEFAULT '',
    message        TEXT NOT NULL DEFAULT '',
    progress       REAL,                            -- 0..1 when measurable
    error          TEXT NOT NULL DEFAULT '',
    result         TEXT NOT NULL DEFAULT '{}',
    ref_id         TEXT,                            -- backup id, update run id, migration id
    started_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    finished_at    INTEGER
);

CREATE UNIQUE INDEX operations_one_exclusive
    ON operations(server_id) WHERE status = 'running' AND exclusive = 1;
CREATE INDEX operations_server_recent ON operations(server_id, started_at DESC);
CREATE INDEX operations_running ON operations(status) WHERE status = 'running';

-- +goose Down
DROP TABLE IF EXISTS operations;
