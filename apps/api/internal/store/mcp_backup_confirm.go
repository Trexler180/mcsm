package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ── Backup confirmations ─────────────────────────────────────────
//
// The MCP facade's create_server_backup asks a human mid-call, through the
// protocol's elicitation round trip, and will not start a backup without an
// answer. The state that ties the answer to the question lives here.
//
// It is a credential, not a label: whoever holds it can turn one agent request
// into a backup. So it is minted at random rather than derived, stored as a
// hash, redeemed at most once, and expires on its own — a human "yes" answers
// the backup it was asked about and no other.

// MCPBackupConfirmationLifetime bounds how long an unanswered confirmation
// stays usable. It is a dialog sitting in front of an operator, so this is
// generous for that and far too short to be worth stealing.
const MCPBackupConfirmationLifetime = 10 * time.Minute

var (
	// ErrMCPConfirmationInvalid is the single answer for every unusable
	// confirmation state — unknown, expired, already used, or issued for a
	// different grant or server. Callers must not let a client tell them apart.
	ErrMCPConfirmationInvalid = errors.New("backup confirmation is not usable")
)

// mcpConfirmPrefix keeps this value distinguishable from the token families in
// mcp.go if one ever turns up in a log. It is not a bearer token and is never
// accepted as one.
const mcpConfirmPrefix = "mcsm_mcpk_"

// CreateMCPBackupConfirmation mints the state for one pending confirmation and
// returns it. The raw value exists only in the elicitation that carries it.
func (s *Store) CreateMCPBackupConfirmation(ctx context.Context, grantID, serverID string) (string, error) {
	state, hash, err := generateMCPToken(mcpConfirmPrefix)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_backup_confirmations (state_hash, grant_id, server_id, created_at, expires_at)
		 VALUES (?,?,?,?,?)`,
		hash, grantID, serverID, now, now.Add(MCPBackupConfirmationLifetime),
	); err != nil {
		return "", err
	}
	return state, nil
}

// ConsumeMCPBackupConfirmation redeems a confirmation for exactly the grant and
// server it was issued against.
//
// The conditional UPDATE is the single-use guarantee, and it runs before the
// caller is told anything: a confirmation that loses this race was already
// spent, whoever presented it.
func (s *Store) ConsumeMCPBackupConfirmation(ctx context.Context, state, grantID, serverID string) error {
	if state == "" {
		return ErrMCPConfirmationInvalid
	}
	hash := HashMCPToken(state)

	var storedGrant, storedServer string
	var expiresAt time.Time
	var consumedAt *time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT grant_id, server_id, expires_at, consumed_at
		   FROM mcp_backup_confirmations WHERE state_hash = ?`, hash,
	).Scan(&storedGrant, &storedServer, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMCPConfirmationInvalid
	}
	if err != nil {
		return ErrMCPConfirmationInvalid
	}
	// Bind before consuming. A confirmation presented for the wrong grant or
	// the wrong server proves nothing, so it must not be able to burn a state
	// that a legitimate caller is still holding.
	if storedGrant != grantID || storedServer != serverID {
		return ErrMCPConfirmationInvalid
	}
	if consumedAt != nil || !expiresAt.After(time.Now()) {
		return ErrMCPConfirmationInvalid
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE mcp_backup_confirmations SET consumed_at = ?
		  WHERE state_hash = ? AND consumed_at IS NULL`, time.Now().UTC(), hash)
	if err != nil {
		return ErrMCPConfirmationInvalid
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMCPConfirmationInvalid
	}
	return nil
}
