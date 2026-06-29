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
	if !strings.HasPrefix(raw, encTokenPrefix) {
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
