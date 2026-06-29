-- +goose Up

-- Optional TOTP multi-factor auth. The secret is stored encrypted at rest
-- (AES-GCM via the app encryption key); recovery codes are stored as a JSON
-- array of SHA-256 hashes so a DB leak doesn't reveal usable codes.
ALTER TABLE users ADD COLUMN totp_secret TEXT;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_recovery TEXT;

-- Session metadata so a user can review and revoke their active sessions.
ALTER TABLE refresh_tokens ADD COLUMN user_agent TEXT;
ALTER TABLE refresh_tokens ADD COLUMN ip TEXT;
ALTER TABLE refresh_tokens ADD COLUMN last_used_at DATETIME;

-- Attribute a scheduled task to its creator so the scheduler can re-authorize it
-- at fire time (a task must not outlive its creator's permission to run it).
ALTER TABLE scheduled_tasks ADD COLUMN created_by TEXT REFERENCES users(id);

-- +goose Down

ALTER TABLE scheduled_tasks DROP COLUMN created_by;
ALTER TABLE refresh_tokens DROP COLUMN last_used_at;
ALTER TABLE refresh_tokens DROP COLUMN ip;
ALTER TABLE refresh_tokens DROP COLUMN user_agent;
ALTER TABLE users DROP COLUMN totp_recovery;
ALTER TABLE users DROP COLUMN totp_enabled;
ALTER TABLE users DROP COLUMN totp_secret;
