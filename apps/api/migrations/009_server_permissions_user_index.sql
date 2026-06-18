-- +goose Up

CREATE INDEX server_permissions_user_id ON server_permissions(user_id);

-- +goose Down

DROP INDEX IF EXISTS server_permissions_user_id;
