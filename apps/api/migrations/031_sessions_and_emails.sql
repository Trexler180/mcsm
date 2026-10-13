-- +goose Up

-- Refresh-token rotation grace. Two tabs share one cookie jar, so when both
-- refresh at once the loser presents the token the winner just rotated away.
-- Remembering the previous hash for a short window lets that request get an
-- access token instead of logging the user out. A presentation after the
-- window is treated as reuse of a stolen token and ends the session.
-- rotated_at_unix is unix seconds so it compares without date-format quirks.
ALTER TABLE refresh_tokens ADD COLUMN previous_hash TEXT;
ALTER TABLE refresh_tokens ADD COLUMN rotated_at_unix INTEGER;
CREATE INDEX refresh_tokens_previous_hash ON refresh_tokens(previous_hash)
    WHERE previous_hash IS NOT NULL;

-- Email addresses are matched case-insensitively at login, but the column was
-- unique case-sensitively, so "Alice@x" and "alice@x" could both exist and
-- login picked one arbitrarily. Keep the oldest account's address and park any
-- later duplicate under a visibly altered one an administrator can correct.
UPDATE users
   SET email = lower(trim(email)) || '.duplicate-' || substr(id, 1, 8)
 WHERE EXISTS (
        SELECT 1 FROM users AS older
         WHERE lower(trim(older.email)) = lower(trim(users.email))
           AND (older.created_at < users.created_at
                OR (older.created_at = users.created_at AND older.id < users.id)));
UPDATE users SET email = lower(trim(email));
CREATE UNIQUE INDEX users_email_lower ON users(lower(email));

-- The only global roles are admin and user. "operator" was accepted but never
-- granted anything; per-server presets cover what it was meant to be.
UPDATE users SET role = 'user' WHERE role NOT IN ('admin', 'user');

-- +goose Down
DROP INDEX IF EXISTS users_email_lower;
DROP INDEX IF EXISTS refresh_tokens_previous_hash;
ALTER TABLE refresh_tokens DROP COLUMN rotated_at_unix;
ALTER TABLE refresh_tokens DROP COLUMN previous_hash;
