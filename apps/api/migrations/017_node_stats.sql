-- +goose Up
-- Live node stats reported by the agent on each heartbeat. All nullable: a
-- node that has never been polled (or runs an older agent) simply has no data.
ALTER TABLE nodes ADD COLUMN mem_used_mb INTEGER;
ALTER TABLE nodes ADD COLUMN disk_used_gb INTEGER;
ALTER TABLE nodes ADD COLUMN cpu_pct REAL;
ALTER TABLE nodes ADD COLUMN uptime_seconds INTEGER;
ALTER TABLE nodes ADD COLUMN os TEXT;
ALTER TABLE nodes ADD COLUMN arch TEXT;
ALTER TABLE nodes ADD COLUMN agent_version TEXT;

-- +goose Down
ALTER TABLE nodes DROP COLUMN mem_used_mb;
ALTER TABLE nodes DROP COLUMN disk_used_gb;
ALTER TABLE nodes DROP COLUMN cpu_pct;
ALTER TABLE nodes DROP COLUMN uptime_seconds;
ALTER TABLE nodes DROP COLUMN os;
ALTER TABLE nodes DROP COLUMN arch;
ALTER TABLE nodes DROP COLUMN agent_version;
