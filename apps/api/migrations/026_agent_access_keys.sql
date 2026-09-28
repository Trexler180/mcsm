-- +goose Up
-- Turn the reserved `api_keys` table into a real capability: a scoped,
-- expiring, revocable machine credential that external automation presents as
-- `Authorization: Bearer mcsm_pat_…`.
--
-- Every column added here is part of the fail-closed contract. A key is only
-- usable when it names at least one server, at least one scope, and an expiry
-- in the future — so the rows that already exist (empty arrays by DEFAULT, and
-- historically a NULL expires_at) authenticate as nothing without any backfill.
--
--   token_prefix  non-secret display fragment ("mcsm_pat_AbCdEf12") so an
--                 operator can match a key in the UI to the one their agent
--                 holds. The secret itself is only ever stored as the existing
--                 SHA-256 token_hash, whose UNIQUE constraint is preserved.
--   scopes        JSON array of server permissions, normalized against the same
--                 vocabulary server_permissions uses. `admin` is refused at the
--                 store layer, so a key can never administer membership.
--   server_ids    JSON array; the key's server allowlist. Checked before the
--                 owner's own RBAC, so an admin-owned key stays bounded.
--   last_used_at  bounded usage metadata, written only when stale (see
--                 TouchAccessKey) so sustained polling doesn't write per call.
--   last_used_ip
--   revoked_at    soft revocation: the row (and its audit history) survives.
ALTER TABLE api_keys ADD COLUMN token_prefix TEXT NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN scopes TEXT NOT NULL DEFAULT '[]';
ALTER TABLE api_keys ADD COLUMN server_ids TEXT NOT NULL DEFAULT '[]';
ALTER TABLE api_keys ADD COLUMN last_used_at DATETIME;
ALTER TABLE api_keys ADD COLUMN last_used_ip TEXT;
ALTER TABLE api_keys ADD COLUMN revoked_at DATETIME;

-- Owner inventory: the Security page lists a user's keys newest-first.
CREATE INDEX api_keys_user_id ON api_keys(user_id, created_at DESC);
-- Authentication looks a presented hash up on every machine request. token_hash
-- is already UNIQUE; this partial index keeps the live set (which excludes
-- revoked rows) small and lets the planner skip them.
CREATE INDEX api_keys_active_token ON api_keys(token_hash) WHERE revoked_at IS NULL;

-- Audit attribution. user_id keeps naming the human who owns the credential;
-- api_key_id names which of their credentials acted. NULL means a human session
-- or a background/system action, so existing rows and writers stay valid.
-- ON DELETE SET NULL mirrors audit_log.user_id: deleting a user cascades their
-- keys away but must not erase the history of what those keys did.
ALTER TABLE audit_log ADD COLUMN api_key_id TEXT REFERENCES api_keys(id) ON DELETE SET NULL;

-- +goose Down
DROP INDEX IF EXISTS api_keys_active_token;
DROP INDEX IF EXISTS api_keys_user_id;

ALTER TABLE audit_log DROP COLUMN api_key_id;

ALTER TABLE api_keys DROP COLUMN revoked_at;
ALTER TABLE api_keys DROP COLUMN last_used_ip;
ALTER TABLE api_keys DROP COLUMN last_used_at;
ALTER TABLE api_keys DROP COLUMN server_ids;
ALTER TABLE api_keys DROP COLUMN scopes;
ALTER TABLE api_keys DROP COLUMN token_prefix;
