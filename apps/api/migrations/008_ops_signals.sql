-- +goose Up
-- Persistent record of detected mod conflicts. Conflicts are detected from the
-- console crash output (incompatible-mods list or a startup crash); previously
-- they lived only in the browser session, so the cockpit had no way to ask
-- "which servers have an unresolved conflict right now?". A row is active while
-- resolved_at IS NULL and is marked resolved once the offending jars are
-- disabled.
CREATE TABLE mod_conflicts (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL DEFAULT '',   -- 'incompatible' | 'crash'
    summary     TEXT NOT NULL DEFAULT '',
    mods        TEXT NOT NULL DEFAULT '[]', -- JSON array of involved mod names
    detected_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME
);

CREATE INDEX mod_conflicts_active ON mod_conflicts(server_id, resolved_at);

-- Indexed log warnings/errors so the cockpit can show "latest warnings" without
-- streaming the full console. Today these are written by the poller when it
-- detects a crash or failed start; the agent forwarding parsed console WARN/
-- ERROR lines is a future enhancement that will populate the same table.
CREATE TABLE server_log_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id  TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    level      TEXT NOT NULL DEFAULT 'warn', -- 'warn' | 'error'
    message    TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',     -- 'poller' | 'console'
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX server_log_events_recent ON server_log_events(server_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS server_log_events;
DROP TABLE IF EXISTS mod_conflicts;
