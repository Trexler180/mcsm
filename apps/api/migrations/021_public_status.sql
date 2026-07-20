-- +goose Up
-- Opt-in public status page. A server becomes publicly visible only when
-- public_status is set AND a slug is chosen; the slug doubles as the subdomain
-- label (<slug>.<status domain>) so it follows DNS-label rules, enforced at
-- the API layer. The partial unique index leaves legacy ''/NULL rows alone.
ALTER TABLE servers ADD COLUMN public_status INTEGER NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN public_slug TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX servers_public_slug
    ON servers(public_slug) WHERE public_slug != '';

-- +goose Down
DROP INDEX IF EXISTS servers_public_slug;
ALTER TABLE servers DROP COLUMN public_slug;
ALTER TABLE servers DROP COLUMN public_status;
