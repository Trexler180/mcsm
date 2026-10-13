-- +goose Up
-- How many mod updates a server had the last time anyone looked. Checking is
-- a round trip to Modrinth and friends, far too slow for the fleet summary,
-- so the summary reports the last known count and when it was taken.
CREATE TABLE mod_update_checks (
    server_id  TEXT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    available  INTEGER NOT NULL DEFAULT 0,
    checked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS mod_update_checks;
