-- +goose Up
-- Per-user alert notifications. The panel already detects the events operators
-- care about (crashes, status flips, conflicts, backup/update outcomes, node
-- heartbeat loss) but had no way to tell anyone. These tables let each user
-- subscribe to the alerts they want and receive them through an in-app live
-- feed, browser Web Push, and outbound webhooks. Everything is scoped to a user
-- and (for server-scoped events) re-checked against their live permissions at
-- emit time, so an alert never reveals a server the user can no longer see.

-- A subscription is one rule: "tell me about <event_type> [on <server>] at or
-- above <min_severity> through <channels>". server_id NULL means "every server
-- I can access". channels is a JSON array of channel selectors:
-- "inapp", "webpush", or "webhook:<channel_id>".
CREATE TABLE notification_subscriptions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    server_id   TEXT REFERENCES servers(id) ON DELETE CASCADE, -- NULL = all accessible
    min_severity TEXT NOT NULL DEFAULT 'info',  -- 'info' | 'warning' | 'critical'
    channels    TEXT NOT NULL DEFAULT '[]',     -- JSON array of channel selectors
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One row per (user, event_type, scope). A NULL server_id can't participate in a
-- UNIQUE constraint the way a value does (NULLs are distinct in SQLite), so the
-- all-servers rule is kept unique with a partial index and the scoped rules with
-- another. Upserts target whichever applies.
CREATE UNIQUE INDEX notification_subscriptions_scoped
    ON notification_subscriptions(user_id, event_type, server_id)
    WHERE server_id IS NOT NULL;
CREATE UNIQUE INDEX notification_subscriptions_all
    ON notification_subscriptions(user_id, event_type)
    WHERE server_id IS NULL;

-- A webhook delivery endpoint owned by a user. The HMAC signing secret is NOT
-- stored here — it lives encrypted-at-rest in app_secrets under
-- "webhook_secret:<id>" and is never returned to the client.
CREATE TABLE notification_channels (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL DEFAULT 'webhook', -- room for future kinds
    label       TEXT NOT NULL DEFAULT '',
    config      TEXT NOT NULL DEFAULT '{}',      -- JSON: {url, format}
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX notification_channels_user ON notification_channels(user_id);

-- A registered browser push subscription (one per device/browser). endpoint is
-- the push service URL; p256dh/auth are the client's encryption keys. failures
-- counts consecutive delivery errors so a dead device can be pruned after the
-- push service reports it gone (404/410).
CREATE TABLE webpush_devices (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint       TEXT NOT NULL UNIQUE,
    p256dh         TEXT NOT NULL,
    auth           TEXT NOT NULL,
    user_agent     TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_success_at DATETIME,
    failures       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX webpush_devices_user ON webpush_devices(user_id);

-- A delivered notification. This row IS the in-app feed item (read_at NULL =
-- unread) and the canonical record from which external deliveries are spawned.
-- dedupe_key collapses repeats (e.g. a crash loop) within a per-type cooldown.
CREATE TABLE notifications (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'info',
    server_id   TEXT,                  -- not FK: keep the feed item if server is deleted
    node_id     TEXT,
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    data        TEXT NOT NULL DEFAULT '{}', -- JSON: structured event payload
    dedupe_key  TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    read_at     DATETIME
);

CREATE INDEX notifications_feed ON notifications(user_id, created_at DESC);
CREATE INDEX notifications_unread ON notifications(user_id, read_at);
CREATE INDEX notifications_dedupe ON notifications(user_id, dedupe_key, created_at DESC);

-- The durable outbox: one row per external delivery attempt of a notification.
-- The dispatcher claims due 'pending' rows, sends them, and either marks 'sent'
-- or schedules a retry with exponential backoff. Because the whole queue is in
-- SQLite, pending work survives an API restart. target_kind is 'webhook' or
-- 'webpush'; target_id is the channel id or the webpush device id.
CREATE TABLE notification_deliveries (
    id              TEXT PRIMARY KEY,
    notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_kind     TEXT NOT NULL,  -- 'webhook' | 'webpush'
    target_id       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending|sent|failed|skipped
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX notification_deliveries_due ON notification_deliveries(status, next_attempt_at);

-- +goose Down
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS webpush_devices;
DROP TABLE IF EXISTS notification_channels;
DROP TABLE IF EXISTS notification_subscriptions;
