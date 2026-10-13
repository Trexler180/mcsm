package store

import (
	"context"
	"strings"
	"testing"
)

func TestNodeTokenEncryptedRoundTrip(t *testing.T) {
	s := testStore(t).WithEncryption("node-token-master")
	ctx := context.Background()

	const token = "super-secret-agent-token"
	n, err := s.EnsureNode(ctx, "n1", "localhost", 8090, "http", token)
	if err != nil {
		t.Fatalf("EnsureNode: %v", err)
	}

	// GetNode must return the original plaintext.
	got, err := s.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Token != token {
		t.Fatalf("token round-trip mismatch: got %q want %q", got.Token, token)
	}

	// The raw column must be ciphertext, not the plaintext token.
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT token FROM nodes WHERE id = ?`, n.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw token: %v", err)
	}
	if !strings.HasPrefix(raw, encBoundPrefix) {
		t.Fatalf("stored token is not marked encrypted: %q", raw)
	}
	if strings.Contains(raw, token) {
		t.Fatal("plaintext token leaked into the stored column")
	}
}

func TestNodeTokenLegacyPlaintextPassthrough(t *testing.T) {
	s := testStore(t).WithEncryption("node-token-master")
	ctx := context.Background()

	n, err := s.EnsureNode(ctx, "legacy", "localhost", 8090, "http", "placeholder")
	if err != nil {
		t.Fatalf("EnsureNode: %v", err)
	}
	// Simulate a pre-encryption row by writing a bare plaintext token.
	if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET token = ? WHERE id = ?`, "bare-plaintext", n.ID); err != nil {
		t.Fatalf("inject plaintext: %v", err)
	}
	got, err := s.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Token != "bare-plaintext" {
		t.Fatalf("legacy plaintext not passed through: got %q", got.Token)
	}
}

// A token copied from one node row into another must not decrypt there: the
// ciphertext is bound to the node it was written for.
func TestNodeTokenBoundToItsRow(t *testing.T) {
	s := testStore(t).WithEncryption("node-token-master")
	ctx := context.Background()
	a, _ := s.EnsureNode(ctx, "a", "localhost", 8090, "http", "token-a")
	b, _ := s.EnsureNode(ctx, "b", "localhost", 8091, "http", "token-b")
	var rawA string
	if err := s.db.QueryRowContext(ctx, `SELECT token FROM nodes WHERE id = ?`, a.ID).Scan(&rawA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET token = ? WHERE id = ?`, rawA, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNode(ctx, b.ID); err == nil {
		t.Fatal("a ciphertext moved to another row decrypted")
	}
}

// Values in the older unbound form, and plaintext ones, are rewritten into the
// bound form at startup without changing what they decrypt to.
func TestUpgradeEncryptionBindsLegacyValues(t *testing.T) {
	s := testStore(t).WithEncryption("node-token-master")
	ctx := context.Background()
	n, _ := s.EnsureNode(ctx, "legacy", "localhost", 8090, "http", "x")
	legacy, err := encryptSecret(s.secretKey, "legacy-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET token = ? WHERE id = ?`, encTokenPrefix+legacy, n.ID); err != nil {
		t.Fatal(err)
	}
	upgraded, err := s.UpgradeEncryption(ctx)
	if err != nil || upgraded != 1 {
		t.Fatalf("UpgradeEncryption = %d, %v", upgraded, err)
	}
	var raw string
	_ = s.db.QueryRowContext(ctx, `SELECT token FROM nodes WHERE id = ?`, n.ID).Scan(&raw)
	if !strings.HasPrefix(raw, encBoundPrefix) {
		t.Fatalf("token still unbound after upgrade: %q", raw)
	}
	got, err := s.GetNode(ctx, n.ID)
	if err != nil || got.Token != "legacy-token" {
		t.Fatalf("upgraded token = %q, %v", got.Token, err)
	}
	if again, _ := s.UpgradeEncryption(ctx); again != 0 {
		t.Fatalf("a second upgrade rewrote %d values", again)
	}
}
