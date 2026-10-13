package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// encTokenPrefix marks a value encrypted at rest without a binding to its row
// (the original format). Values written now use encBoundPrefix. Plaintext rows
// (written before encryption, or by an encryption-disabled dev instance) carry
// neither and are read back transparently; every form is upgraded to the bound
// one by UpgradeEncryption.
const (
	encTokenPrefix = "enc:v1:"
	encBoundPrefix = "enc:v2:"
	// appSecretBoundPrefix marks a bound app_secrets value; unprefixed values
	// there are the legacy unbound form.
	appSecretBoundPrefix = "v2:"
)

// Where an encrypted value lives, as associated data.
func nodeTokenAAD(nodeID string) []byte  { return []byte("node_token:" + nodeID) }
func totpSecretAAD(userID string) []byte { return []byte("totp_secret:" + userID) }
func appSecretAAD(key string) []byte     { return []byte("app_secret:" + key) }

// encryptAtRest returns the at-rest representation of a sensitive value: a
// prefixed AES-GCM ciphertext bound to aad when an encryption key is
// configured, or the raw value when none is (dev, no secret). Used for
// credentials we must store but that a DB leak should not immediately expose:
// agent tokens and TOTP secrets.
func (s *Store) encryptAtRest(v string, aad []byte) (string, error) {
	if len(s.secretKey) == 0 {
		return v, nil
	}
	enc, err := encryptSecret(s.secretKey, v, aad)
	if err != nil {
		return "", err
	}
	return encBoundPrefix + enc, nil
}

// decryptAtRest reverses encryptAtRest for every stored form.
func (s *Store) decryptAtRest(stored string, aad []byte) (string, error) {
	if rest, ok := strings.CutPrefix(stored, encBoundPrefix); ok {
		if len(s.secretKey) == 0 {
			return "", errNoEncryptionKey
		}
		return decryptSecret(s.secretKey, rest, aad)
	}
	if rest, ok := strings.CutPrefix(stored, encTokenPrefix); ok {
		if len(s.secretKey) == 0 {
			return "", errNoEncryptionKey
		}
		return decryptSecret(s.secretKey, rest, nil)
	}
	return stored, nil
}

// EncryptNodeToken and DecryptNodeToken encrypt agent tokens at rest, bound to
// the node they belong to.
func (s *Store) EncryptNodeToken(nodeID, token string) (string, error) {
	return s.encryptAtRest(token, nodeTokenAAD(nodeID))
}

func (s *Store) DecryptNodeToken(nodeID, stored string) (string, error) {
	return s.decryptAtRest(stored, nodeTokenAAD(nodeID))
}

func (s *Store) encryptAppSecret(key, plaintext string) (string, error) {
	enc, err := encryptSecret(s.secretKey, plaintext, appSecretAAD(key))
	if err != nil {
		return "", err
	}
	return appSecretBoundPrefix + enc, nil
}

func (s *Store) decryptAppSecret(key, stored string) (string, error) {
	if rest, ok := strings.CutPrefix(stored, appSecretBoundPrefix); ok {
		return decryptSecret(s.secretKey, rest, appSecretAAD(key))
	}
	return decryptSecret(s.secretKey, stored, nil)
}

// UpgradeEncryption rewrites every encrypted value still in an unbound form, or
// stored in plaintext, into the bound form. It runs at startup and rewrites
// each value at most once. A value it cannot read is left untouched and
// reported, so a wrong master key never destroys data.
func (s *Store) UpgradeEncryption(ctx context.Context) (upgraded int, err error) {
	if len(s.secretKey) == 0 {
		return 0, nil
	}
	type row struct{ id, value string }
	collect := func(q string) ([]row, error) {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.value); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	var failures []string
	nodes, err := collect(`SELECT id, token FROM nodes WHERE token NOT LIKE 'enc:v2:%'`)
	if err != nil {
		return 0, err
	}
	for _, n := range nodes {
		plain, err := s.decryptAtRest(n.value, nil)
		if err != nil {
			failures = append(failures, "node "+n.id)
			continue
		}
		enc, err := s.EncryptNodeToken(n.id, plain)
		if err != nil {
			return upgraded, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET token = ? WHERE id = ?`, enc, n.id); err != nil {
			return upgraded, err
		}
		upgraded++
	}

	users, err := collect(`SELECT id, totp_secret FROM users
	                        WHERE totp_secret IS NOT NULL AND totp_secret != '' AND totp_secret NOT LIKE 'enc:v2:%'`)
	if err != nil {
		return upgraded, err
	}
	for _, u := range users {
		plain, err := s.decryptAtRest(u.value, nil)
		if err != nil {
			failures = append(failures, "user "+u.id)
			continue
		}
		enc, err := s.encryptAtRest(plain, totpSecretAAD(u.id))
		if err != nil {
			return upgraded, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret = ? WHERE id = ?`, enc, u.id); err != nil {
			return upgraded, err
		}
		upgraded++
	}

	secrets, err := collect(`SELECT key, value_encrypted FROM app_secrets WHERE value_encrypted NOT LIKE 'v2:%'`)
	if err != nil {
		return upgraded, err
	}
	for _, sec := range secrets {
		plain, err := decryptSecret(s.secretKey, sec.value, nil)
		if err != nil {
			failures = append(failures, "secret "+sec.id)
			continue
		}
		enc, err := s.encryptAppSecret(sec.id, plain)
		if err != nil {
			return upgraded, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE app_secrets SET value_encrypted = ? WHERE key = ?`, enc, sec.id); err != nil {
			return upgraded, err
		}
		upgraded++
	}
	if len(failures) > 0 {
		return upgraded, fmt.Errorf("could not read %d stored secret(s) with the current key (%s); left unchanged",
			len(failures), strings.Join(failures, ", "))
	}
	return upgraded, nil
}

// SetSecret encrypts plaintext and upserts it under key. updatedBy is recorded
// for the audit trail; pass "" when unknown.
func (s *Store) SetSecret(ctx context.Context, key, plaintext, updatedBy string) error {
	if key == "" {
		return errors.New("secret key required")
	}
	enc, err := s.encryptAppSecret(key, plaintext)
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
	return s.decryptAppSecret(key, enc)
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
