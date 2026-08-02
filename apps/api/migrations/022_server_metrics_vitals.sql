-- +goose Up
-- Helper-mod vitals (TPS / MSPT) alongside the existing resource samples. The
-- Fabric helper mod pushes a vitals snapshot to the agent every 15s; the poller
-- reads it once a minute and stores it on the same row as the CPU/RAM sample.
--
-- All vitals columns are NULLable on purpose: NULL means "no mod data for this
-- sample" (mod absent, link down, or the snapshot was stale), which is a
-- different fact from a genuine TPS of 0 during a freeze. Keeping them distinct
-- lets AVG()/COUNT() skip missing samples instead of dragging averages to zero,
-- and lets the chart draw a gap rather than a cliff.
ALTER TABLE server_metrics ADD COLUMN tps      REAL;
ALTER TABLE server_metrics ADD COLUMN mspt_avg REAL;
ALTER TABLE server_metrics ADD COLUMN mspt_p95 REAL;

-- Hourly rollup counterparts. As with cpu_max, the extremes matter more than
-- the mean for diagnosis, but the interesting extreme differs per metric:
--   tps_min      — TPS pathology is a *dip*, so the minimum preserves lag spikes
--                  the way cpu_max preserves load peaks.
--   mspt_p95_max — the worst 95th-percentile tick time seen in the hour; a
--                  sustained bad tail survives averaging into coarse buckets.
-- vitals_samples counts only the samples that actually carried vitals, so the
-- history query can weight hourly means correctly and tell "no data" apart from
-- "zero" even when an hour mixes linked and unlinked samples.
ALTER TABLE server_metrics_hourly ADD COLUMN tps_avg        REAL;
ALTER TABLE server_metrics_hourly ADD COLUMN tps_min        REAL;
ALTER TABLE server_metrics_hourly ADD COLUMN mspt_avg       REAL;
ALTER TABLE server_metrics_hourly ADD COLUMN mspt_p95_max   REAL;
ALTER TABLE server_metrics_hourly ADD COLUMN vitals_samples INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE server_metrics_hourly DROP COLUMN vitals_samples;
ALTER TABLE server_metrics_hourly DROP COLUMN mspt_p95_max;
ALTER TABLE server_metrics_hourly DROP COLUMN mspt_avg;
ALTER TABLE server_metrics_hourly DROP COLUMN tps_min;
ALTER TABLE server_metrics_hourly DROP COLUMN tps_avg;
ALTER TABLE server_metrics DROP COLUMN mspt_p95;
ALTER TABLE server_metrics DROP COLUMN mspt_avg;
ALTER TABLE server_metrics DROP COLUMN tps;
