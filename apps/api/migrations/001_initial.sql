-- +goose Up

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login DATETIME
);

CREATE TABLE refresh_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    fqdn TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 8090,
    scheme TEXT NOT NULL DEFAULT 'http',
    token TEXT NOT NULL,
    memory_mb INTEGER,
    disk_gb INTEGER,
    cpu_cores INTEGER,
    location TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen DATETIME
);

CREATE TABLE servers (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id),
    owner_id TEXT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    description TEXT,
    platform TEXT NOT NULL DEFAULT 'paper',
    mc_version TEXT NOT NULL DEFAULT '1.21.4',
    loader_version TEXT,
    directory_path TEXT NOT NULL,
    java_binary TEXT NOT NULL DEFAULT 'java',
    jvm_args TEXT NOT NULL DEFAULT '[]',
    port INTEGER NOT NULL DEFAULT 25565,
    ram_mb_min INTEGER NOT NULL DEFAULT 512,
    ram_mb_max INTEGER NOT NULL DEFAULT 2048,
    status TEXT NOT NULL DEFAULT 'offline',
    auto_start INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '[]',
    settings TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX servers_node_id ON servers(node_id);
CREATE INDEX servers_owner_id ON servers(owner_id);

CREATE TABLE server_permissions (
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permissions TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (server_id, user_id)
);

CREATE TABLE installed_mods (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    source_id TEXT,
    version_id TEXT,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    file_name TEXT NOT NULL,
    sha256 TEXT,
    pinned INTEGER NOT NULL DEFAULT 0,
    installed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX installed_mods_server_id ON installed_mods(server_id);

CREATE TABLE backup_targets (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    retention TEXT,
    is_default INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE backups (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    target_id TEXT REFERENCES backup_targets(id),
    triggered_by TEXT REFERENCES users(id),
    trigger TEXT NOT NULL DEFAULT 'manual',
    status TEXT NOT NULL DEFAULT 'running',
    size_bytes INTEGER,
    snapshot_id TEXT,
    metadata TEXT,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE INDEX backups_server_id ON backups(server_id);

CREATE TABLE scheduled_tasks (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    action TEXT NOT NULL,
    payload TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run DATETIME,
    next_run DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id),
    server_id TEXT REFERENCES servers(id),
    action TEXT NOT NULL,
    detail TEXT,
    ip_address TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX audit_log_server_id ON audit_log(server_id, created_at DESC);
CREATE INDEX audit_log_user_id ON audit_log(user_id, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS scheduled_tasks;
DROP TABLE IF EXISTS backups;
DROP TABLE IF EXISTS backup_targets;
DROP TABLE IF EXISTS installed_mods;
DROP TABLE IF EXISTS server_permissions;
DROP TABLE IF EXISTS servers;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
