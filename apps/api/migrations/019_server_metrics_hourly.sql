-- +goose Up
-- Hourly rollups of server_metrics, kept forever. The sampler folds the raw
-- one-minute samples into these buckets before the 30-day raw prune runs, so
-- long-range history (the stats page's 90d/1y/all-time windows) survives raw
-- retention at hourly resolution — about 9k rows per server-year, a few hundred
-- KB. Averages stay recombinable into coarser buckets via the samples count
-- (weighted mean); maxes preserve the peaks that averaging would erase.
CREATE TABLE server_metrics_hourly (
    server_id    TEXT    NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    ts           INTEGER NOT NULL, -- hour bucket start, unix seconds
    cpu_avg      REAL    NOT NULL DEFAULT 0,
    cpu_max      REAL    NOT NULL DEFAULT 0,
    ram_avg_mb   INTEGER NOT NULL DEFAULT 0,
    ram_max_mb   INTEGER NOT NULL DEFAULT 0,
    ram_total_mb INTEGER NOT NULL DEFAULT 0,
    players_avg  REAL    NOT NULL DEFAULT 0,
    players_max  INTEGER NOT NULL DEFAULT 0,
    samples      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, ts)
);

-- +goose Down
DROP TABLE IF EXISTS server_metrics_hourly;
