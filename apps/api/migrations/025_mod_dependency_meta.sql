-- +goose Up
-- The dependency graph was only ever written while installing a mod through the
-- panel, from that mod's own metadata, and only for required dependencies. That
-- leaves out every jar that arrived another way (uploaded by hand, adopted from
-- disk, recognized by hash) and can't tell "breaks without this" apart from
-- "optionally uses this" — both of which the removal-impact check needs.
--
-- Record the declared type per edge, and remember the mod set a server's graph
-- was last rebuilt from so the rebuild is skipped while nothing has changed.
ALTER TABLE mod_dependencies ADD COLUMN dependency_type TEXT NOT NULL DEFAULT 'required';

CREATE TABLE mod_dependency_scans (
    server_id   TEXT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    -- Digest of the installed (source, project, version) set the scan covered.
    -- A different digest means something was installed, updated or removed, so
    -- the declared dependencies may have changed and the graph is rebuilt.
    fingerprint TEXT NOT NULL,
    scanned_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS mod_dependency_scans;
ALTER TABLE mod_dependencies DROP COLUMN dependency_type;
