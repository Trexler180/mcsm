-- +goose Up
-- Subscribe existing users to whitelist-rejection alerts.
--
-- Every other alert in this system is opt-in, and rightly so: the operator can
-- always go find out that a backup failed or a node went quiet. This one is
-- different because the subject is a person waiting at the door — they give up
-- and go elsewhere long before anyone thinks to look for a notification setting
-- they never knew existed. An alert nobody has enabled is, for this event, the
-- same as no feature.
--
-- Rules are inserted only where the user has none for this event, so anyone who
-- has already expressed a preference (including turning it off, which leaves an
-- explicit disabled row) keeps it.
INSERT INTO notification_subscriptions (id, user_id, event_type, server_id, min_severity, channels, enabled)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-a' ||
       substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))),
       u.id, 'player.join_denied', NULL, 'info', '["inapp"]', 1
  FROM users u
 WHERE NOT EXISTS (
       SELECT 1 FROM notification_subscriptions ns
        WHERE ns.user_id = u.id
          AND ns.event_type = 'player.join_denied'
          AND ns.server_id IS NULL);

-- +goose Down
DELETE FROM notification_subscriptions WHERE event_type = 'player.join_denied';
