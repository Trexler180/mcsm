-- +goose Up
-- Store each jar's sha512 so the panel can recognize imported jars against
-- Modrinth's file index (their files are keyed by sha1/sha512). A non-null value
-- means the file has already been hashed and looked up, so reconciliation never
-- rehashes the same jar on every Mods-tab load.
ALTER TABLE installed_mods ADD COLUMN sha512 TEXT;

-- +goose Down
ALTER TABLE installed_mods DROP COLUMN sha512;
