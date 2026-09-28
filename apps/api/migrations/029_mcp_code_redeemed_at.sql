-- +goose Up
-- Tell an authorization code that is mid-exchange apart from one whose exchange
-- has already finished.
--
-- `consumed_at` is the atomic claim: exactly one caller ever sets it, and that
-- is what makes a code single-use. It is not enough on its own to decide what a
-- *second* presentation means. A client that double-submits its token exchange
-- produces two presentations that overlap, and treating the loser as theft
-- revoked the delegation the winner was still establishing — so neither caller
-- ended up with a working token, and an honest retry destroyed the connection
-- it was trying to make.
--
-- `redeemed_at` is set once the winning redemption has finished. A presentation
-- that finds it NULL overlapped an exchange still in flight and fails quietly;
-- one that finds it set is a genuine replay, and retires the grant as before.
-- The distinction is a recorded fact rather than a clock comparison, because
-- the whole of a redemption can complete inside one tick of the system clock.
ALTER TABLE mcp_authorization_codes ADD COLUMN redeemed_at DATETIME;

-- +goose Down
ALTER TABLE mcp_authorization_codes DROP COLUMN redeemed_at;
