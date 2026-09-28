-- +goose Up
-- Remote MCP: an OAuth 2.1 delegation model for AI agents the operator already
-- runs (Claude Code, Codex), so pointing one at this panel never requires
-- installing a connector or pasting a reusable secret.
--
-- These tables are deliberately separate from `api_keys`. The two credential
-- families answer different questions — an access key is a secret its owner
-- carries, a grant is a delegation its owner made — and keeping them apart
-- means neither can be presented where the other is expected. What they share
-- is the *authorization* vocabulary: scopes here are the same server
-- permissions server_permissions uses, checked against the owner's live RBAC on
-- every call.

-- A registered MCP client. Public clients only: Claude Code and Codex run on
-- the operator's own machine and cannot keep a secret, so there is no secret
-- column. Issuing a pretend secret to a client that cannot protect one is worse
-- than issuing none, because it invites treating the client as confidential.
CREATE TABLE mcp_clients (
    client_id          TEXT PRIMARY KEY,
    client_name        TEXT NOT NULL,
    -- JSON array. Matched exactly at /authorize and /token; no prefix,
    -- wildcard, or normalization matching, which is where redirect bypasses
    -- come from.
    redirect_uris      TEXT NOT NULL DEFAULT '[]',
    -- 'dynamic' (RFC 7591) or 'preregistered'. Shown on the consent screen so
    -- a human can tell a self-registered client from a configured one.
    origin             TEXT NOT NULL DEFAULT 'dynamic',
    software_id        TEXT,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_registered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One in-flight /authorize call, parked while a human decides. Everything the
-- consent screen and the eventual code need is frozen here at request time, so
-- nothing the client sends later can change what was actually approved.
CREATE TABLE mcp_authorization_requests (
    id                    TEXT PRIMARY KEY,
    client_id             TEXT NOT NULL REFERENCES mcp_clients(client_id) ON DELETE CASCADE,
    redirect_uri          TEXT NOT NULL,
    state                 TEXT,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL,
    scopes                TEXT NOT NULL DEFAULT '[]',
    -- The canonicalized MCP endpoint this authorization is for. Carried into
    -- the token so an access token minted for this resource cannot be replayed
    -- against another one.
    resource              TEXT NOT NULL,
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at            DATETIME NOT NULL,
    resolved_at           DATETIME
);
CREATE INDEX mcp_authorization_requests_expiry ON mcp_authorization_requests(expires_at);

-- A durable delegation: this user let this client reach these servers with
-- these capabilities until this date. Revoking it is the single act that stops
-- every token beneath it.
CREATE TABLE mcp_grants (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id    TEXT NOT NULL REFERENCES mcp_clients(client_id) ON DELETE CASCADE,
    -- Snapshot of the client's name at consent time. A client that renames
    -- itself later must not be able to rewrite what the owner was shown.
    client_name  TEXT NOT NULL,
    scopes       TEXT NOT NULL DEFAULT '[]',
    server_ids   TEXT NOT NULL DEFAULT '[]',
    resource     TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME NOT NULL,
    revoked_at   DATETIME,
    last_used_at DATETIME,
    last_used_ip TEXT
);
CREATE INDEX mcp_grants_user ON mcp_grants(user_id, created_at DESC);

-- Single-use authorization codes. Stored as a hash like every other credential
-- here; `consumed_at` is set by a conditional UPDATE, so two simultaneous
-- redemptions cannot both win.
CREATE TABLE mcp_authorization_codes (
    code_hash  TEXT PRIMARY KEY,
    request_id TEXT NOT NULL REFERENCES mcp_authorization_requests(id) ON DELETE CASCADE,
    grant_id   TEXT NOT NULL REFERENCES mcp_grants(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME
);

-- Access and refresh tokens. `family_id` ties a refresh token to its
-- ancestors: presenting a rotated (already-used) refresh token means the token
-- leaked, so the whole family is revoked rather than just the replayed one.
CREATE TABLE mcp_tokens (
    id         TEXT PRIMARY KEY,
    grant_id   TEXT NOT NULL REFERENCES mcp_grants(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    family_id  TEXT NOT NULL,
    resource   TEXT NOT NULL,
    scopes     TEXT NOT NULL DEFAULT '[]',
    issued_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME,
    used_at    DATETIME
);
CREATE INDEX mcp_tokens_grant ON mcp_tokens(grant_id, kind);
CREATE INDEX mcp_tokens_family ON mcp_tokens(family_id);

-- The action approval queue. An agent files a request; a human approves it in
-- the dashboard with a password/TOTP step-up; only then does anything happen.
-- The status column is the concurrency control: every transition out of
-- 'pending' is a conditional UPDATE, so an action executes at most once however
-- many times it is approved or polled.
CREATE TABLE mcp_action_requests (
    id             TEXT PRIMARY KEY,
    grant_id       TEXT NOT NULL REFERENCES mcp_grants(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id      TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    -- 'start' | 'stop' | 'restart'. Nothing else is requestable in this phase.
    action         TEXT NOT NULL,
    -- The agent's stated justification. Model-authored text: displayed to the
    -- approver as untrusted evidence, never interpreted.
    reason         TEXT NOT NULL DEFAULT '',
    -- 'pending' | 'approved' | 'denied' | 'expired' | 'executing' | 'executed' | 'failed'
    status         TEXT NOT NULL DEFAULT 'pending',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at     DATETIME NOT NULL,
    decided_at     DATETIME,
    decided_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    executed_at    DATETIME,
    failure_reason TEXT
);
CREATE INDEX mcp_action_requests_owner ON mcp_action_requests(user_id, status, created_at DESC);
CREATE INDEX mcp_action_requests_grant ON mcp_action_requests(grant_id, created_at DESC);

-- Audit attribution for the third actor kind. user_id still names the human who
-- delegated; api_key_id names an access key; mcp_grant_id names the delegation
-- an AI agent acted under. ON DELETE SET NULL mirrors the other two: revoking or
-- deleting a grant must not erase the history of what it did.
ALTER TABLE audit_log ADD COLUMN mcp_grant_id TEXT REFERENCES mcp_grants(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE audit_log DROP COLUMN mcp_grant_id;

DROP INDEX IF EXISTS mcp_action_requests_grant;
DROP INDEX IF EXISTS mcp_action_requests_owner;
DROP TABLE IF EXISTS mcp_action_requests;

DROP INDEX IF EXISTS mcp_tokens_family;
DROP INDEX IF EXISTS mcp_tokens_grant;
DROP TABLE IF EXISTS mcp_tokens;

DROP TABLE IF EXISTS mcp_authorization_codes;

DROP INDEX IF EXISTS mcp_grants_user;
DROP TABLE IF EXISTS mcp_grants;

DROP INDEX IF EXISTS mcp_authorization_requests_expiry;
DROP TABLE IF EXISTS mcp_authorization_requests;

DROP TABLE IF EXISTS mcp_clients;
