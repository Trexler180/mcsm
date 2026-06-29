package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// TOTPConfig is a user's current MFA state. Secret is decrypted and only handed
// out to the auth layer (login verification, enrollment), never serialized to a
// client.
type TOTPConfig struct {
	Secret  string
	Enabled bool
}

// GetUserTOTP returns the user's TOTP secret (decrypted) and whether MFA is
// enabled. A user with no secret yet returns ("", false, nil).
func (s *Store) GetUserTOTP(ctx context.Context, userID string) (TOTPConfig, error) {
	var enc sql.NullString
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT totp_secret, totp_enabled FROM users WHERE id = ?`, userID,
	).Scan(&enc, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return TOTPConfig{}, fmt.Errorf("user not found")
	}
	if err != nil {
		return TOTPConfig{}, err
	}
	if !enc.Valid || enc.String == "" {
		return TOTPConfig{Enabled: false}, nil
	}
	secret, err := s.decryptAtRest(enc.String)
	if err != nil {
		return TOTPConfig{}, err
	}
	return TOTPConfig{Secret: secret, Enabled: enabled}, nil
}

// SetUserTOTPSecret stores a freshly generated (not-yet-verified) secret,
// encrypted at rest, leaving MFA disabled until the user confirms a code.
func (s *Store) SetUserTOTPSecret(ctx context.Context, userID, secret string) error {
	enc, err := s.encryptAtRest(secret)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_enabled = 0, totp_recovery = NULL WHERE id = ?`,
		enc, userID)
	return err
}

// EnableUserTOTP flips MFA on and stores the SHA-256 hashes of the recovery
// codes (one-time display happens at the call site).
func (s *Store) EnableUserTOTP(ctx context.Context, userID string, recoveryHashes []string) error {
	blob, err := json.Marshal(recoveryHashes)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_enabled = 1, totp_recovery = ? WHERE id = ? AND totp_secret IS NOT NULL`,
		string(blob), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no pending TOTP secret to enable")
	}
	return nil
}

// DisableUserTOTP clears all MFA state for a user (self-service with a verified
// code, or an admin reset for a locked-out user).
func (s *Store) DisableUserTOTP(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = NULL, totp_enabled = 0, totp_recovery = NULL WHERE id = ?`,
		userID)
	return err
}

// ConsumeRecoveryCode checks code against the user's stored recovery hashes and,
// on a match, removes it (single use). Returns true when a code was consumed.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, code string) (bool, error) {
	var blob sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT totp_recovery FROM users WHERE id = ?`, userID).Scan(&blob); err != nil {
		return false, err
	}
	if !blob.Valid || blob.String == "" {
		return false, nil
	}
	var hashes []string
	if err := json.Unmarshal([]byte(blob.String), &hashes); err != nil {
		return false, err
	}
	want := hashRecoveryCode(code)
	idx := -1
	for i, h := range hashes {
		// Constant-time compare so a partial match can't be timed out.
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	remaining := append(hashes[:idx:idx], hashes[idx+1:]...)
	updated, err := json.Marshal(remaining)
	if err != nil {
		return false, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_recovery = ? WHERE id = ?`, string(updated), userID); err != nil {
		return false, err
	}
	return true, nil
}

// hashRecoveryCode hashes a recovery code for storage/comparison. Recovery codes
// are high-entropy random strings, so a fast SHA-256 is sufficient (no need for
// a slow KDF the way a human-chosen password would).
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// HashRecoveryCode is the exported form used when generating fresh codes.
func HashRecoveryCode(code string) string { return hashRecoveryCode(code) }
