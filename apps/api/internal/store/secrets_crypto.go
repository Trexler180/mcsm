package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// errNoEncryptionKey is returned by the secret methods when no master key was
// configured (neither APP_ENCRYPTION_KEY nor JWT_SECRET at boot). Storing a
// secret under an empty key would be worse than refusing, so we refuse.
var errNoEncryptionKey = errors.New("secret storage is not configured: set APP_ENCRYPTION_KEY (or JWT_SECRET)")

// secretKeyLabel domain-separates the derived encryption key from any other use
// of the same master secret (e.g. JWT signing), so the two can never collide.
const secretKeyLabel = "mcsm/app-secrets/v1"

// deriveKey turns an arbitrary-length master secret into a 32-byte AES-256 key.
// An empty master yields nil, which the callers treat as "encryption disabled".
func deriveKey(master string) []byte {
	if master == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(secretKeyLabel + ":" + master))
	return sum[:]
}

// encryptSecret seals plaintext with AES-256-GCM under key and returns
// base64(nonce|ciphertext|tag). A fresh random nonce is prepended on every call.
func encryptSecret(key []byte, plaintext string) (string, error) {
	if len(key) == 0 {
		return "", errNoEncryptionKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptSecret reverses encryptSecret. A wrong key or tampered ciphertext
// fails authentication and returns an error rather than garbage.
func decryptSecret(key []byte, encoded string) (string, error) {
	if len(key) == 0 {
		return "", errNoEncryptionKey
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("stored secret is malformed")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}
