-- +goose Up
-- Servers can be put in an order of the operator's choosing (drag to reorder),
-- within their folder or among the ungrouped ones. Existing servers start in
-- the order the panel showed them before: by name.
ALTER TABLE servers ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
UPDATE servers SET sort_order = (
    SELECT COUNT(*) FROM servers s2
    WHERE coalesce(s2.folder_id, '') = coalesce(servers.folder_id, '')
      AND (lower(s2.name) < lower(servers.name) OR (lower(s2.name) = lower(servers.name) AND s2.id < servers.id))
);

-- +goose Down
ALTER TABLE servers DROP COLUMN sort_order;
