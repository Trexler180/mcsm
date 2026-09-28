-- +goose Up
-- Single-use confirmations for the one MCP tool that asks a human mid-call.
--
-- create_server_backup does not act on the agent's word. It returns an MCP
-- elicitation, the client puts the question to its operator, and the answer
-- comes back on a second call carrying the opaque state the server issued.
--
-- That state used to be a fixed hash of (grant, server): the same value every
-- time, valid forever. One human "yes" could therefore be replayed for every
-- future backup on that server, which is precisely what the tool's own
-- description promises will not happen. Storing the state makes the promise
-- enforceable — it is minted at random, redeemed at most once, and expires.
--
-- Only the hash is stored, like every other credential here: the row is enough
-- to verify a confirmation that comes back, and not enough to forge one.
CREATE TABLE mcp_backup_confirmations (
    state_hash  TEXT PRIMARY KEY,
    grant_id    TEXT NOT NULL REFERENCES mcp_grants(id) ON DELETE CASCADE,
    server_id   TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL,
    consumed_at DATETIME
);
CREATE INDEX mcp_backup_confirmations_expiry ON mcp_backup_confirmations(expires_at);

-- +goose Down
DROP INDEX IF EXISTS mcp_backup_confirmations_expiry;
DROP TABLE IF EXISTS mcp_backup_confirmations;
