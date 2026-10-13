-- +goose Up
-- Deleting a server used to delete its whole audit history: audit_log.server_id
-- was ON DELETE CASCADE, so the record of everything done to a server — who
-- restored it, who ran which command, who deleted it — vanished with it.
--
-- server_id keeps the id with no foreign key at all, so rows about a deleted
-- server still group by server and still read as "about that server" (SET NULL
-- would have turned them into panel-wide rows). server_name snapshots the name
-- at write time, so the history stays readable after the server is gone.
--
-- SQLite can't ALTER a foreign key, so the table is rebuilt (as in 005/014).
-- Nothing references audit_log, so the rebuild is safe inside goose's txn.
CREATE TABLE audit_log_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    server_id TEXT,
    server_name TEXT,
    action TEXT NOT NULL,
    detail TEXT,
    ip_address TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    api_key_id TEXT REFERENCES api_keys(id) ON DELETE SET NULL,
    mcp_grant_id TEXT REFERENCES mcp_grants(id) ON DELETE SET NULL
);
INSERT INTO audit_log_new (id, user_id, server_id, server_name, action, detail, ip_address, created_at, api_key_id, mcp_grant_id)
    SELECT a.id, a.user_id, a.server_id, s.name, a.action, a.detail, a.ip_address, a.created_at, a.api_key_id, a.mcp_grant_id
    FROM audit_log a LEFT JOIN servers s ON s.id = a.server_id;
DROP TABLE audit_log;
ALTER TABLE audit_log_new RENAME TO audit_log;
CREATE INDEX audit_log_server_id ON audit_log(server_id, created_at DESC);
CREATE INDEX audit_log_user_id ON audit_log(user_id, created_at DESC);
CREATE INDEX audit_log_api_key_id ON audit_log(api_key_id) WHERE api_key_id IS NOT NULL;
CREATE INDEX audit_log_mcp_grant_id ON audit_log(mcp_grant_id) WHERE mcp_grant_id IS NOT NULL;

-- +goose Down
-- Back to the cascading key. Rows about servers that no longer exist cannot
-- satisfy it and are dropped, which is what the old schema would have done.
-- The credential indexes are not recreated: 026/027 roll back by dropping
-- those columns, which SQLite refuses while an index names them.
CREATE TABLE audit_log_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    server_id TEXT REFERENCES servers(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    detail TEXT,
    ip_address TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    api_key_id TEXT REFERENCES api_keys(id) ON DELETE SET NULL,
    mcp_grant_id TEXT REFERENCES mcp_grants(id) ON DELETE SET NULL
);
INSERT INTO audit_log_old (id, user_id, server_id, action, detail, ip_address, created_at, api_key_id, mcp_grant_id)
    SELECT id, user_id, server_id, action, detail, ip_address, created_at, api_key_id, mcp_grant_id
    FROM audit_log
    WHERE server_id IS NULL OR server_id IN (SELECT id FROM servers);
DROP TABLE audit_log;
ALTER TABLE audit_log_old RENAME TO audit_log;
CREATE INDEX audit_log_server_id ON audit_log(server_id, created_at DESC);
CREATE INDEX audit_log_user_id ON audit_log(user_id, created_at DESC);
