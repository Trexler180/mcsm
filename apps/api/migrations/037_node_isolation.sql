-- +goose Up
-- What each node's agent reports about server isolation: whether servers run
-- as the dedicated server user, and why not. JSON as the agent sends it;
-- NULL until an agent that reports it has been polled.
ALTER TABLE nodes ADD COLUMN isolation TEXT;

-- +goose Down
ALTER TABLE nodes DROP COLUMN isolation;
