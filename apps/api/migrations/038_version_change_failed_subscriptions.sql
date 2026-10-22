-- +goose Up
-- Subscribe existing users to alerts for a version change that did not land.
--
-- A failed version change puts the server back on its own, and the crash of
-- its trial boot happens inside the run, so no crash alert goes out for it.
-- Without this alert the only trace is a line on the server's Version page, and
-- nobody looks there for news. New users get it from DefaultOnTypes; this
-- backfills everyone who predates it.
--
-- Rules are inserted only where the user has none for this event, so anyone who
-- has already expressed a preference keeps it.
INSERT INTO notification_subscriptions (id, user_id, event_type, server_id, min_severity, channels, enabled)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-a' ||
       substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))),
       u.id, 'server.version_change_failed', NULL, 'info', '["inapp"]', 1
  FROM users u
 WHERE NOT EXISTS (
       SELECT 1 FROM notification_subscriptions ns
        WHERE ns.user_id = u.id
          AND ns.event_type = 'server.version_change_failed'
          AND ns.server_id IS NULL);

-- +goose Down
DELETE FROM notification_subscriptions WHERE event_type = 'server.version_change_failed';
