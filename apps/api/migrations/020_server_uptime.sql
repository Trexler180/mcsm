-- +goose Up
-- One row per online stretch of a server, maintained on the status-transition
-- boundary in UpdateServerStatus (poller, lifecycle handlers, migrate and
-- auto-update engines all funnel through it). ended_at NULL marks the server
-- still online; end_reason distinguishes a panel-initiated stop from a crash
-- the poller detected. Timestamps are unix seconds like the metrics tables.
CREATE TABLE server_uptime (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id  TEXT    NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,
    end_reason TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX server_uptime_server_started
    ON server_uptime(server_id, started_at DESC);

CREATE INDEX server_uptime_open
    ON server_uptime(server_id) WHERE ended_at IS NULL;

-- Servers already online when this feature arrives get an open segment
-- anchored at migration time: "up since at least now" — honest, rather than
-- guessing a start time from updated_at (bumped by any settings edit).
INSERT INTO server_uptime (server_id, started_at)
SELECT id, CAST(strftime('%s', 'now') AS INTEGER)
FROM servers WHERE status = 'online';

-- +goose Down
DROP TABLE IF EXISTS server_uptime;
