package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SecretMeta describes a stored secret without ever exposing its value. hint is
// the last few characters of the plaintext (for a masked "••••1234" display).
type SecretMeta struct {
	Key        string    `json:"key"`
	Configured bool      `json:"configured"`
	Hint       string    `json:"hint"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// hintOf returns the trailing characters of a secret for masked display,
// without revealing enough to be useful to an onlooker. Short secrets yield "".
func hintOf(plaintext string) string {
	if len(plaintext) < 8 {
		return ""
	}
	return plaintext[len(plaintext)-4:]
}

// SetSecret encrypts plaintext and upserts it under key. updatedBy is recorded
// for the audit trail; pass "" when unknown.
func (s *Store) SetSecret(ctx context.Context, key, plaintext, updatedBy string) error {
	if key == "" {
		return errors.New("secret key required")
	}
	enc, err := encryptSecret(s.secretKey, plaintext)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_secrets (key, value_encrypted, hint, updated_at, updated_by)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(key) DO UPDATE SET
		  value_encrypted = excluded.value_encrypted,
		  hint            = excluded.hint,
		  updated_at      = CURRENT_TIMESTAMP,
		  updated_by      = excluded.updated_by`,
		key, enc, hintOf(plaintext), updatedBy)
	return err
}

// GetSecret returns the decrypted plaintext for key, or ("", nil) when no such
// secret is stored. A decrypt failure (wrong master key, tampering) is an error.
func (s *Store) GetSecret(ctx context.Context, key string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx,
		`SELECT value_encrypted FROM app_secrets WHERE key = ?`, key).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return decryptSecret(s.secretKey, enc)
}

// DeleteSecret removes a stored secret. Deleting a missing key is a no-op.
func (s *Store) DeleteSecret(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_secrets WHERE key = ?`, key)
	return err
}

// ListSecretMeta returns metadata for every stored secret. Values are never
// decrypted or returned.
func (s *Store) ListSecretMeta(ctx context.Context) ([]SecretMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, hint, updated_at FROM app_secrets ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretMeta{}
	for rows.Next() {
		var m SecretMeta
		if err := rows.Scan(&m.Key, &m.Hint, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Configured = true
		out = append(out, m)
	}
	return out, rows.Err()
}
