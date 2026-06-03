-- +goose Up
-- Track whether a mod jar is enabled. Disabling renames the file to
-- "<name>.disabled" on disk (Minecraft loaders ignore those), so the mod stays
-- installed but is not loaded by the server.
ALTER TABLE installed_mods ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE installed_mods DROP COLUMN enabled;
