-- +goose Up
-- Per-server resource samples, written by the poller's history sampler (about
-- one row per minute per running server) and pruned after a retention window.
-- ts is unix seconds; the composite primary key keeps range scans over
-- (server, window) index-only and makes re-inserted samples idempotent.
CREATE TABLE server_metrics (
    server_id    TEXT    NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    ts           INTEGER NOT NULL,
    cpu_percent  REAL    NOT NULL DEFAULT 0,
    ram_used_mb  INTEGER NOT NULL DEFAULT 0,
    ram_total_mb INTEGER NOT NULL DEFAULT 0,
    players      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, ts)
);

-- +goose Down
DROP TABLE IF EXISTS server_metrics;
