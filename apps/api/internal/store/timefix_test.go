package store

import (
	"context"
	"testing"
	"time"
)

func TestParseStoredTimeReadsEveryFormat(t *testing.T) {
	want := time.Date(2026, 1, 2, 18, 4, 5, 0, time.UTC)
	for _, v := range []string{
		"2026-01-02 11:04:05 -0700 PDT",
		"2026-01-02 11:04:05.25 -0700 PDT m=+3.104",
		"2026-01-02 18:04:05+00:00",
		"2026-01-02T18:04:05Z",
		"2026-01-02T20:04:05.5+02:00",
		"2026-01-02 18:04:05.123",
	} {
		got, ok := parseStoredTime(v)
		if !ok || got.Truncate(time.Second) != want {
			t.Errorf("%q -> %v, %v", v, got, ok)
		}
	}
}

// Rows written in the old formats are rewritten as canonical UTC text, so
// they compare correctly with CURRENT_TIMESTAMP and with each other.
func TestNormalizeTimesRewritesOldRows(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u, err := s.CreateUser(ctx, "someone@example.com", "h", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, "2026-01-02 11:04:05.25 -0700 PDT m=+3.104", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO audit_log (action, created_at) VALUES ('x', '2026-01-02T18:04:05Z'), ('y', 'not a time')`); err != nil {
		t.Fatal(err)
	}

	n, err := s.NormalizeTimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rewrote %d values, want 2", n)
	}
	var login, created string
	_ = s.db.QueryRow(`SELECT last_login || '' FROM users WHERE id = ?`, u.ID).Scan(&login)
	_ = s.db.QueryRow(`SELECT created_at || '' FROM audit_log WHERE action = 'x'`).Scan(&created)
	if login != "2026-01-02 18:04:05" || created != "2026-01-02 18:04:05" {
		t.Fatalf("last_login %q, created_at %q", login, created)
	}
	// Idempotent; the unreadable value is left alone rather than lost.
	if n, err := s.NormalizeTimes(ctx); err != nil || n != 0 {
		t.Fatalf("second pass rewrote %d (%v)", n, err)
	}
	var odd string
	_ = s.db.QueryRow(`SELECT created_at || '' FROM audit_log WHERE action = 'y'`).Scan(&odd)
	if odd != "not a time" {
		t.Fatalf("unreadable value became %q", odd)
	}
}

// With the production DSN settings, a Go time is stored exactly like
// CURRENT_TIMESTAMP.
func TestGoTimesAreStoredAsCanonicalUTC(t *testing.T) {
	s := testStore(t)
	local := time.Date(2026, 1, 2, 11, 4, 5, 0, time.FixedZone("PDT", -7*3600))
	if _, err := s.db.Exec(`INSERT INTO audit_log (action, created_at) VALUES ('z', ?)`, local); err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = s.db.QueryRow(`SELECT created_at || '' FROM audit_log WHERE action = 'z'`).Scan(&raw)
	if raw != "2026-01-02 18:04:05" {
		t.Fatalf("stored as %q", raw)
	}
}
