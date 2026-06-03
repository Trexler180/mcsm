-- +goose Up
-- Reverse-dependency graph: one row per "dependent requires dependency" edge,
-- scoped to a server. Lets us answer "what still requires this mod?" so an
-- auto-installed dependency can be flagged orphaned once nothing points at it.
-- Keyed by Modrinth/CurseForge project id (installed_mods.source_id).
CREATE TABLE mod_dependencies (
    server_id             TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    dependent_project_id  TEXT NOT NULL,
    dependency_project_id TEXT NOT NULL,
    PRIMARY KEY (server_id, dependent_project_id, dependency_project_id)
);

CREATE INDEX mod_dependencies_dependency
    ON mod_dependencies(server_id, dependency_project_id);

-- +goose Down
DROP TABLE IF EXISTS mod_dependencies;
