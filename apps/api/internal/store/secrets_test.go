package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// encStore returns a test store with encryption enabled under a fixed master.
func encStore(t *testing.T) *Store {
	t.Helper()
	return testStore(t).WithEncryption("test-master-secret")
}

func TestSecretRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)

	const key = "curseforge_api_key"
	const val = "cf-super-secret-value-1234"
	if err := s.SetSecret(ctx, key, val, "tester"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSecret(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != val {
		t.Fatalf("GetSecret = %q, want %q", got, val)
	}

	// The stored column must be ciphertext, not the plaintext.
	var stored string
	if err := s.db.QueryRowContext(ctx,
		`SELECT value_encrypted FROM app_secrets WHERE key = ?`, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, val) {
		t.Fatalf("value stored in cleartext: %q", stored)
	}
}

func TestGetSecretAbsent(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)
	got, err := s.GetSecret(ctx, "nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("GetSecret(absent) = %q, want empty", got)
	}
}

func TestSetSecretUpsertAndHint(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)

	if err := s.SetSecret(ctx, "k", "first-value-aaaa", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret(ctx, "k", "second-value-bbbb", "u2"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSecret(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "second-value-bbbb" {
		t.Fatalf("upsert failed, GetSecret = %q", got)
	}

	meta, err := s.ListSecretMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 {
		t.Fatalf("ListSecretMeta len = %d, want 1", len(meta))
	}
	m := meta[0]
	if m.Key != "k" || !m.Configured {
		t.Fatalf("unexpected meta: %+v", m)
	}
	if m.Hint != "bbbb" {
		t.Fatalf("Hint = %q, want last 4 chars %q", m.Hint, "bbbb")
	}
}

func TestListSecretMetaNeverLeaksValue(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)
	const val = "leak-check-value-zzzz"
	if err := s.SetSecret(ctx, "k", val, ""); err != nil {
		t.Fatal(err)
	}
	meta, err := s.ListSecretMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meta {
		if strings.Contains(m.Hint, "leak") {
			t.Fatalf("hint exposes too much of value: %q", m.Hint)
		}
	}
}

func TestDeleteSecret(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)
	if err := s.SetSecret(ctx, "k", "value-to-delete", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSecret(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSecret(ctx, "k")
	if err != nil || got != "" {
		t.Fatalf("after delete GetSecret = %q, err = %v", got, err)
	}
	// Deleting a missing key is a no-op, not an error.
	if err := s.DeleteSecret(ctx, "k"); err != nil {
		t.Fatalf("delete of missing key errored: %v", err)
	}
}

func TestDecryptUnderWrongMasterFails(t *testing.T) {
	ctx := context.Background()
	s := encStore(t)
	if err := s.SetSecret(ctx, "k", "value", ""); err != nil {
		t.Fatal(err)
	}
	// Rotate the master secret: existing ciphertext must no longer decrypt.
	s.WithEncryption("a-different-master")
	if _, err := s.GetSecret(ctx, "k"); err == nil {
		t.Fatal("expected decrypt failure under wrong master, got nil")
	}
}

func TestSecretsRequireEncryptionKey(t *testing.T) {
	ctx := context.Background()
	s := testStore(t) // no WithEncryption → encryption disabled
	if err := s.SetSecret(ctx, "k", "value", ""); !errors.Is(err, errNoEncryptionKey) {
		t.Fatalf("SetSecret without key error = %v, want errNoEncryptionKey", err)
	}
}
