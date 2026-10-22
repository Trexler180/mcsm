-- +goose Up

-- The time step of the last authenticator code accepted for each user, so the
-- same code can't be used twice while it is still current (RFC 6238 §5.2).
-- NULL until a code is next accepted.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER;

-- +goose Down

ALTER TABLE users DROP COLUMN totp_last_step;
