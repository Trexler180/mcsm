-- +goose Up
-- Deleting a user failed with FOREIGN KEY constraint failed (787) whenever the
-- user was still referenced by rows that had no ON DELETE action:
--   * audit_log.user_id        (their actions were logged)
--   * backups.triggered_by     (they triggered a backup)
--   * scheduled_tasks.created_by (they created a task)
-- SQLite can't ALTER a FK, so rebuild each table with the right action. None of
-- these tables are referenced by other tables, so the rebuild is safe inside
-- goose's txn (same approach as migration 005).
--
-- Choice of action per column:
--   * user_id / triggered_by -> SET NULL: these are historical records; keep the
--     row, just forget who the (now-deleted) user was.
--   * created_by -> CASCADE: a scheduled task must not outlive its creator's
--     permission to run it (see 012). Leaving it as SET NULL would be unsafe:
--     the scheduler treats a NULL creator as a legacy/always-authorized task,
--     so an orphaned task would run with no authorization check. Delete it.

-- ── backups: triggered_by -> ON DELETE SET NULL ──────────────────────────────
CREATE TABLE backups_new (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    target_id TEXT REFERENCES backup_targets(id),
    triggered_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    trigger TEXT NOT NULL DEFAULT 'manual',
    status TEXT NOT NULL DEFAULT 'running',
    size_bytes INTEGER,
    snapshot_id TEXT,
    metadata TEXT,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);
INSERT INTO backups_new
    SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes,
           snapshot_id, metadata, started_at, completed_at FROM backups;
DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX backups_server_id ON backups(server_id);

-- ── scheduled_tasks: created_by -> ON DELETE CASCADE ─────────────────────────
CREATE TABLE scheduled_tasks_new (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    action TEXT NOT NULL,
    payload TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run DATETIME,
    next_run DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT REFERENCES users(id) ON DELETE CASCADE
);
INSERT INTO scheduled_tasks_new
    SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run,
           next_run, created_at, created_by FROM scheduled_tasks;
DROP TABLE scheduled_tasks;
ALTER TABLE scheduled_tasks_new RENAME TO scheduled_tasks;

-- ── audit_log: user_id -> ON DELETE SET NULL (server_id stays CASCADE) ────────
CREATE TABLE audit_log_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
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
-- Restore the original no-action user FKs.

CREATE TABLE backups_old (
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
INSERT INTO backups_old
    SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes,
           snapshot_id, metadata, started_at, completed_at FROM backups;
DROP TABLE backups;
ALTER TABLE backups_old RENAME TO backups;
CREATE INDEX backups_server_id ON backups(server_id);

CREATE TABLE scheduled_tasks_old (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    action TEXT NOT NULL,
    payload TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run DATETIME,
    next_run DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT REFERENCES users(id)
);
INSERT INTO scheduled_tasks_old
    SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run,
           next_run, created_at, created_by FROM scheduled_tasks;
DROP TABLE scheduled_tasks;
ALTER TABLE scheduled_tasks_old RENAME TO scheduled_tasks;

CREATE TABLE audit_log_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id),
    server_id TEXT REFERENCES servers(id) ON DELETE CASCADE,
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
