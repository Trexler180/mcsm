package store

import (
	"context"
	"testing"
	"time"
)

func mfaTestUser(t *testing.T, s *Store) string {
	t.Helper()
	u, err := s.CreateUser(context.Background(), "mfa@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestTOTPLifecycle(t *testing.T) {
	s := testStore(t).WithEncryption("mfa-master")
	ctx := context.Background()
	uid := mfaTestUser(t, s)

	cfg, err := s.GetUserTOTP(ctx, uid)
	if err != nil || cfg.Enabled {
		t.Fatalf("new user should have MFA disabled: %+v %v", cfg, err)
	}

	if err := s.SetUserTOTPSecret(ctx, uid, "SECRET123"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetUserTOTP(ctx, uid)
	if cfg.Secret != "SECRET123" || cfg.Enabled {
		t.Fatalf("secret stored but not yet enabled, got %+v", cfg)
	}

	// Raw column must be ciphertext, not the plaintext secret.
	var raw string
	_ = s.db.QueryRowContext(ctx, `SELECT totp_secret FROM users WHERE id=?`, uid).Scan(&raw)
	if raw == "SECRET123" {
		t.Fatal("TOTP secret stored in plaintext")
	}

	hashes := []string{HashRecoveryCode("CODEONE"), HashRecoveryCode("CODETWO")}
	if err := s.EnableUserTOTP(ctx, uid, hashes); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetUserTOTP(ctx, uid)
	if !cfg.Enabled {
		t.Fatal("MFA should be enabled")
	}

	// Recovery code is single use.
	ok, err := s.ConsumeRecoveryCode(ctx, uid, "CODEONE")
	if err != nil || !ok {
		t.Fatalf("first use should consume: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.ConsumeRecoveryCode(ctx, uid, "CODEONE"); ok {
		t.Fatal("recovery code must not be reusable")
	}
	if ok, _ := s.ConsumeRecoveryCode(ctx, uid, "WRONG"); ok {
		t.Fatal("unknown recovery code must not match")
	}

	if err := s.DisableUserTOTP(ctx, uid); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetUserTOTP(ctx, uid)
	if cfg.Enabled || cfg.Secret != "" {
		t.Fatalf("disable should clear MFA, got %+v", cfg)
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := mfaTestUser(t, s)

	exp := time.Now().Add(time.Hour)
	id1, err := s.CreateRefreshToken(ctx, uid, "hash1", "Firefox", "1.2.3.4", exp)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateRefreshToken(ctx, uid, "hash2", "Chrome", "5.6.7.8", exp)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := s.ListSessions(ctx, uid)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d (err %v)", len(sessions), err)
	}

	// Revoke one (scoped to the owner).
	ok, err := s.DeleteSessionForUser(ctx, id1, uid)
	if err != nil || !ok {
		t.Fatalf("revoke failed: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.DeleteSessionForUser(ctx, id1, "other-user"); ok {
		t.Fatal("must not revoke another user's session")
	}

	// Revoke all except the current.
	id3, _ := s.CreateRefreshToken(ctx, uid, "hash3", "CLI", "9.9.9.9", exp)
	if err := s.DeleteSessionsForUserExcept(ctx, uid, id2); err != nil {
		t.Fatal(err)
	}
	sessions, _ = s.ListSessions(ctx, uid)
	if len(sessions) != 1 || sessions[0].ID != id2 {
		t.Fatalf("only the kept session should remain, got %+v", sessions)
	}
	_ = id3
}

func TestRotateRefreshTokenKeepsIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := mfaTestUser(t, s)

	id, _ := s.CreateRefreshToken(ctx, uid, "oldhash", "UA", "ip", time.Now().Add(time.Hour))
	if err := s.RotateRefreshToken(ctx, id, "newhash", "ip2", "UA2", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRefreshToken(ctx, "oldhash"); err == nil {
		t.Fatal("old token hash should no longer resolve")
	}
	rt, err := s.GetRefreshToken(ctx, "newhash")
	if err != nil || rt.ID != id {
		t.Fatalf("rotated token should keep the same session id: %v", err)
	}
}
