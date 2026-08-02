-- +goose Up
-- Server folders: a flat, fleet-wide grouping so related servers (a "minigames"
-- set, a staging pair) read as one unit in the panel. Deliberately not a tree —
-- one level covers the grouping people actually ask for without the cycle and
-- path-rewrite handling a hierarchy drags in.
--
-- A server belongs to at most one folder; folder_id NULL means "ungrouped",
-- which is the state every pre-existing server starts in. Dropping a folder is
-- an organizational act, never a destructive one, so the FK sets folder_id back
-- to NULL rather than cascading into the servers themselves.
CREATE TABLE server_folders (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- Accent color for the folder chip; '' means "use the default surface
    -- styling". Validated against a fixed palette at the API layer.
    color TEXT NOT NULL DEFAULT '',
    -- Manual ordering in the servers list; ties break by name.
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Folder names are user-facing labels people type to disambiguate; two folders
-- called "Minigames" and "minigames" would be a UI bug, not a feature.
CREATE UNIQUE INDEX server_folders_name ON server_folders(lower(name));

ALTER TABLE servers ADD COLUMN folder_id TEXT REFERENCES server_folders(id) ON DELETE SET NULL;

CREATE INDEX servers_folder_id ON servers(folder_id);

-- +goose Down
DROP INDEX IF EXISTS servers_folder_id;
ALTER TABLE servers DROP COLUMN folder_id;
DROP INDEX IF EXISTS server_folders_name;
DROP TABLE server_folders;
