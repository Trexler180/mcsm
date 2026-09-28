-- +goose Up
-- Approval policy for MCP action requests.
--
-- 027 made every agent-requested action pass through one fixed gate: a human
-- approves it in the dashboard with a password (plus TOTP when enrolled). That
-- gate is right for a connection you did not set up, and heavy-handed for an
-- agent running on your own machine that you supervise directly. This migration
-- makes the gate configurable at two layers without weakening it by default.
--
-- Layering: an account-level default per user, and an optional per-grant
-- override. The override columns are nullable precisely so "inherit" is a real,
-- distinct state rather than a value that silently pins itself to whatever the
-- default happened to be on the day the grant was made.
--
-- The settings are per-user, not global, because approval already is: only the
-- grant owner can approve their own requests (the API 404s any other user), so
-- the person who can act is the person whose policy applies. A global setting
-- would let one admin disable another admin's step-up.
--
-- Every default here is the secure one, and every existing row inherits it. A
-- deployment that upgrades and changes nothing behaves exactly as it did before.

-- Account-level defaults. A user with no row reads as the defaults below, so
-- there is no backfill and no way for a missing row to mean "unprotected".
CREATE TABLE user_mcp_approval_settings (
    user_id                TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- 1 = approving a request requires a password/TOTP step-up (the 027
    -- behavior). 0 = a single click in the dashboard approves it.
    require_password       INTEGER NOT NULL DEFAULT 1,
    -- 1 = start/stop/restart requests execute on arrival with no human in the
    -- loop. Off by default: an agent that can restart unattended can restart
    -- during a session it cannot see.
    auto_approve_lifecycle INTEGER NOT NULL DEFAULT 0,
    -- Deliberately separate from the lifecycle flag. A version upgrade
    -- reinstalls the runtime, rewrites every managed mod, and can roll the
    -- world back to a restore point; it must never ride along on a toggle whose
    -- stated purpose is "let it restart the server".
    auto_approve_upgrades  INTEGER NOT NULL DEFAULT 0,
    updated_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Per-grant overrides. NULL means inherit the owner's account default, which is
-- what every grant that predates this migration gets.
ALTER TABLE mcp_grants ADD COLUMN require_password INTEGER;
ALTER TABLE mcp_grants ADD COLUMN auto_approve_lifecycle INTEGER;
ALTER TABLE mcp_grants ADD COLUMN auto_approve_upgrades INTEGER;

-- +goose Down
ALTER TABLE mcp_grants DROP COLUMN auto_approve_upgrades;
ALTER TABLE mcp_grants DROP COLUMN auto_approve_lifecycle;
ALTER TABLE mcp_grants DROP COLUMN require_password;

DROP TABLE IF EXISTS user_mcp_approval_settings;
