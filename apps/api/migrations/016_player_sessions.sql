-- +goose Up
-- One row per player visit, opened/closed by the poller's session tracker as
-- players appear in / disappear from a running server's roster (about one-
-- minute resolution). ended_at NULL marks a session still in progress; the
-- tracker closes leftovers when a server stops or the API restarts.
CREATE TABLE player_sessions (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    player_name TEXT NOT NULL,
    player_uuid TEXT NOT NULL DEFAULT '',
    started_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at    DATETIME
);

CREATE INDEX player_sessions_server_started
    ON player_sessions(server_id, started_at DESC);

CREATE INDEX player_sessions_open
    ON player_sessions(server_id) WHERE ended_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS player_sessions;
