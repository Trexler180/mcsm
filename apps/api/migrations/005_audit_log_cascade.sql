-- +goose Up
-- audit_log.server_id originally had no ON DELETE action, so deleting a server
-- that had any audit rows failed with FOREIGN KEY constraint failed (787).
-- SQLite can't ALTER a FK, so rebuild the table with ON DELETE CASCADE.
-- Nothing references audit_log, so the rebuild is safe inside goose's txn.
CREATE TABLE audit_log_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id),
    server_id TEXT REFERENCES servers(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    detail TEXT,
    ip_address TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO audit_log_new (id, user_id, server_id, action, detail, ip_address, created_at)
    SELECT id, user_id, server_id, action, detail, ip_address, created_at FROM audit_log;

DROP TABLE audit_log;
ALTER TABLE audit_log_new RENAME TO audit_log;

CREATE INDEX audit_log_server_id ON audit_log(server_id, created_at DESC);
CREATE INDEX audit_log_user_id ON audit_log(user_id, created_at DESC);

-- +goose Down
CREATE TABLE audit_log_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id),
    server_id TEXT REFERENCES servers(id),
    action TEXT NOT NULL,
    detail TEXT,
    ip_address TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO audit_log_old (id, user_id, server_id, action, detail, ip_address, created_at)
    SELECT id, user_id, server_id, action, detail, ip_address, created_at FROM audit_log;

DROP TABLE audit_log;
ALTER TABLE audit_log_old RENAME TO audit_log;

CREATE INDEX audit_log_server_id ON audit_log(server_id, created_at DESC);
CREATE INDEX audit_log_user_id ON audit_log(user_id, created_at DESC);
