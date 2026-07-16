-- +goose Up
-- Reconciliation used to adopt a leftover jar of an already-tracked project as a
-- second managed row (e.g. a stale old-version jar left behind by a reverted
-- version migration), and the auto-updater would then converge both rows onto
-- the same file — showing exact duplicates in the Mods tab. Remove existing
-- duplicates (keeping the oldest row per project) and enforce uniqueness so a
-- managed project can only be tracked once per server directory.
DELETE FROM installed_mods
WHERE source_id IS NOT NULL
  AND rowid NOT IN (
    SELECT rowid FROM (
      SELECT rowid, ROW_NUMBER() OVER (
        PARTITION BY server_id, source, source_id, install_path
        ORDER BY installed_at, rowid
      ) AS rn
      FROM installed_mods
      WHERE source_id IS NOT NULL
    ) WHERE rn = 1
  );

CREATE UNIQUE INDEX installed_mods_project_unique
  ON installed_mods(server_id, source, source_id, install_path)
  WHERE source_id IS NOT NULL;

-- +goose Down
DROP INDEX installed_mods_project_unique;
