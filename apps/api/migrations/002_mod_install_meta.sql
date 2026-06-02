-- +goose Up
-- Track where each mod jar was placed on disk (mods/plugins/datapacks) so
-- uninstall and update can target the right path, and whether it was pulled in
-- automatically as a dependency (so we can offer cleanup).
ALTER TABLE installed_mods ADD COLUMN install_path TEXT NOT NULL DEFAULT '/mods';
ALTER TABLE installed_mods ADD COLUMN installed_as_dep INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE installed_mods DROP COLUMN installed_as_dep;
ALTER TABLE installed_mods DROP COLUMN install_path;
