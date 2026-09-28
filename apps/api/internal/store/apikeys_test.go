package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
)

// keyFixture builds a store with one user and one server they own — the minimum
// context an access key needs.
func keyFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	ctx := context.Background()
	s := testStore(t)
	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &Server{
		NodeID: node.ID, OwnerID: owner.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, owner.ID, srv.ID
}

func mustCreateKey(t *testing.T, s *Store, userID, serverID string, scopes ...string) (*APIKey, string) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{"view"}
	}
	key, token, err := s.CreateAccessKey(context.Background(), userID, "agent",
		scopes, []string{serverID}, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return key, token
}

// The raw secret must exist only in the create response. What lands in SQLite is
// its SHA-256 plus a short, non-secret display prefix — and nothing the API
// hands back to the owner can carry either the token or the hash.
func TestAccessKeySecretIsOneTimeAndHashedAtRest(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)

	key, token := mustCreateKey(t, s, userID, serverID, "view", "power.restart")

	if !strings.HasPrefix(token, auth.AccessKeyPrefix) {
		t.Fatalf("token %q lacks the reserved prefix", token)
	}
	if len(token) < len(auth.AccessKeyPrefix)+40 {
		t.Fatalf("token is too short to carry 256 bits: %d chars", len(token))
	}

	var storedHash, storedPrefix string
	if err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, token_prefix FROM api_keys WHERE id = ?`, key.ID).
		Scan(&storedHash, &storedPrefix); err != nil {
		t.Fatal(err)
	}
	if storedHash != HashAccessKey(token) {
		t.Fatal("stored hash is not the SHA-256 of the presented token")
	}
	if strings.Contains(storedHash, token) || storedHash == token {
		t.Fatal("the raw token reached the database")
	}
	if !strings.HasPrefix(token, storedPrefix) || len(storedPrefix) >= len(token) {
		t.Fatalf("prefix %q is not a short leading fragment of the token", storedPrefix)
	}

	// Re-reading the key never re-exposes the secret.
	listed, err := s.ListAccessKeys(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d keys, want 1", len(listed))
	}
	if listed[0].TokenPrefix != storedPrefix {
		t.Fatalf("prefix=%q want %q", listed[0].TokenPrefix, storedPrefix)
	}

	// And it authenticates.
	res, err := s.AuthenticateAccessKey(ctx, token)
	if err != nil {
		t.Fatalf("freshly created key should authenticate: %v", err)
	}
	if res.Key.ID != key.ID || res.User.ID != userID {
		t.Fatalf("authenticated as key=%s user=%s", res.Key.ID, res.User.ID)
	}
	if strings.Join(res.Key.Scopes, ",") != "power.restart,view" {
		t.Fatalf("scopes=%v want normalized and sorted", res.Key.Scopes)
	}
}

func TestAccessKeyValidationRejectsUnsafeShapes(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	now := time.Now()

	cases := []struct {
		name      string
		keyName   string
		scopes    []string
		servers   []string
		expiresAt time.Time
		wantErr   error
	}{
		{"no name", "", []string{"view"}, []string{serverID}, now.Add(time.Hour), ErrAccessKeyNameRequired},
		{"no scopes", "a", nil, []string{serverID}, now.Add(time.Hour), ErrAccessKeyNoScopes},
		{"blank scopes", "a", []string{"", "  "}, []string{serverID}, now.Add(time.Hour), ErrAccessKeyNoScopes},
		{"admin scope", "a", []string{"view", "admin"}, []string{serverID}, now.Add(time.Hour), ErrAccessKeyAdminScope},
		{"unknown scope", "a", []string{"root"}, []string{serverID}, now.Add(time.Hour), ErrInvalidServerPermission},
		{"no servers", "a", []string{"view"}, nil, now.Add(time.Hour), ErrAccessKeyNoServers},
		{"missing expiry", "a", []string{"view"}, []string{serverID}, time.Time{}, ErrAccessKeyExpiryMissing},
		{"expiry in the past", "a", []string{"view"}, []string{serverID}, now.Add(-time.Minute), ErrAccessKeyExpiryPast},
		{"expiry beyond the cap", "a", []string{"view"}, []string{serverID}, now.Add(MaxAccessKeyLifetime + time.Hour), ErrAccessKeyExpiryTooFar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.CreateAccessKey(ctx, userID, tc.keyName, tc.scopes, tc.servers, tc.expiresAt)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}

	// The boundary itself is allowed: exactly at the cap is fine.
	if _, _, err := s.CreateAccessKey(ctx, userID, "at the cap", []string{"view"},
		[]string{serverID}, time.Now().Add(MaxAccessKeyLifetime-time.Minute)); err != nil {
		t.Fatalf("a key just inside the cap should be accepted: %v", err)
	}
}

// Every unusable credential state must fail closed, and identically — the
// caller learns only "invalid".
func TestAuthenticateAccessKeyFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	_, token := mustCreateKey(t, s, userID, serverID)

	for _, presented := range []string{
		"",
		"not-a-key",
		auth.AccessKeyPrefix,                    // prefix with no secret
		auth.AccessKeyPrefix + "unknown-secret", // well-formed but unknown
		strings.TrimPrefix(token, auth.AccessKeyPrefix), // right secret, no prefix
		strings.ToUpper(token),
	} {
		if _, err := s.AuthenticateAccessKey(ctx, presented); !errors.Is(err, ErrAccessKeyInvalid) {
			t.Fatalf("presented %q: err=%v want ErrAccessKeyInvalid", presented, err)
		}
	}

	// Expired.
	expired, expiredToken, err := s.CreateAccessKey(ctx, userID, "soon", []string{"view"},
		[]string{serverID}, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).UTC(), expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, expiredToken); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("expired key: err=%v want ErrAccessKeyInvalid", err)
	}

	// Malformed capability metadata must not be interpreted leniently.
	bad, badToken := mustCreateKey(t, s, userID, serverID)
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET scopes = ? WHERE id = ?`, "{not json", bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, badToken); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("malformed scopes: err=%v want ErrAccessKeyInvalid", err)
	}
	// Nor may a hand-edited row smuggle in the admin scope.
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET scopes = ? WHERE id = ?`, `["admin"]`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, badToken); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("admin scope written behind the API: err=%v want ErrAccessKeyInvalid", err)
	}
}

// The rows that predate this feature carry empty capability arrays and no
// expiry. They must authenticate as nothing, with no backfill.
func TestLegacyAPIKeyRowsAreInert(t *testing.T) {
	ctx := context.Background()
	s, userID, _ := keyFixture(t)

	legacyToken := auth.AccessKeyPrefix + "legacy-secret"
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, user_id, token_hash, name) VALUES (?,?,?,?)`,
		"legacy", userID, HashAccessKey(legacyToken), "reserved"); err != nil {
		t.Fatal(err)
	}
	var scopes, servers string
	var expires *time.Time
	if err := s.db.QueryRowContext(ctx,
		`SELECT scopes, server_ids, expires_at FROM api_keys WHERE id = 'legacy'`).
		Scan(&scopes, &servers, &expires); err != nil {
		t.Fatal(err)
	}
	if scopes != "[]" || servers != "[]" || expires != nil {
		t.Fatalf("legacy row defaults = %s/%s/%v, want empty capabilities and no expiry", scopes, servers, expires)
	}
	if _, err := s.AuthenticateAccessKey(ctx, legacyToken); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("legacy row authenticated: %v", err)
	}
}

// Rotation swaps the secret in one committed update: the old token is dead the
// moment it lands, with no overlap window.
func TestRotateAccessKeyInvalidatesTheOldTokenImmediately(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	key, oldToken := mustCreateKey(t, s, userID, serverID)

	// Give it usage metadata so we can prove rotation clears it.
	if err := s.TouchAccessKey(ctx, key, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}

	rotated, newToken, err := s.RotateAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if newToken == oldToken {
		t.Fatal("rotation returned the same secret")
	}
	if _, err := s.AuthenticateAccessKey(ctx, oldToken); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("the old token still authenticates after rotation: %v", err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, newToken); err != nil {
		t.Fatalf("the new token should authenticate: %v", err)
	}
	if rotated.ID != key.ID {
		t.Fatal("rotation must keep the key's identity")
	}
	if rotated.LastUsedAt != nil || rotated.LastUsedIP != nil {
		t.Fatalf("stale usage survived rotation: %v / %v", rotated.LastUsedAt, rotated.LastUsedIP)
	}
	if rotated.TokenPrefix == key.TokenPrefix {
		t.Fatal("the display prefix should follow the new secret")
	}

	// Another user's key id is simply not found.
	stranger, err := s.CreateUser(ctx, "stranger@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateAccessKey(ctx, key.ID, stranger.ID); !errors.Is(err, ErrAccessKeyNotFound) {
		t.Fatalf("cross-user rotate err=%v want ErrAccessKeyNotFound", err)
	}
}

func TestRevokeAccessKeyIsImmediateAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	key, token := mustCreateKey(t, s, userID, serverID)

	if err := s.RevokeAccessKey(ctx, key.ID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, token); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("revoked key still authenticates: %v", err)
	}

	revoked, err := s.GetAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revocation should be recorded on the surviving row")
	}
	firstRevokedAt := *revoked.RevokedAt

	// Idempotent: a retry succeeds and does not move the timestamp.
	if err := s.RevokeAccessKey(ctx, key.ID, userID); err != nil {
		t.Fatalf("second revoke should be a no-op, got %v", err)
	}
	again, err := s.GetAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.RevokedAt.Equal(firstRevokedAt) {
		t.Fatal("re-revoking rewrote revoked_at")
	}

	// A revoked key cannot be brought back by rotation.
	if _, _, err := s.RotateAccessKey(ctx, key.ID, userID); !errors.Is(err, ErrAccessKeyRevoked) {
		t.Fatalf("rotate on a revoked key err=%v want ErrAccessKeyRevoked", err)
	}
	if err := s.RevokeAccessKey(ctx, key.ID, "someone-else"); !errors.Is(err, ErrAccessKeyNotFound) {
		t.Fatalf("cross-user revoke err=%v want ErrAccessKeyNotFound", err)
	}
}

// Deleting a user removes their keys (FK cascade) and the keys stop working,
// but the audit history of what they did survives with the key id nulled out.
func TestDeletingOwnerRemovesKeysButKeepsHistory(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// The key holder owns no servers, so they are deletable.
	holder, err := s.CreateUser(ctx, "holder@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &Server{
		NodeID: node.ID, OwnerID: owner.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, token := mustCreateKey(t, s, holder.ID, srv.ID)
	if err := s.LogActionWithKey(ctx, holder.ID, key.ID, srv.ID, "server.restart", "203.0.113.7", nil); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteUser(ctx, holder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAccessKey(ctx, token); !errors.Is(err, ErrAccessKeyInvalid) {
		t.Fatalf("a deleted user's key still authenticates: %v", err)
	}
	entries, err := s.ListAudit(ctx, srv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "server.restart" {
		t.Fatalf("audit history did not survive the delete: %+v", entries)
	}
	if entries[0].UserID != nil || entries[0].APIKeyID != nil {
		t.Fatalf("deleted references should be nulled, got user=%v key=%v", entries[0].UserID, entries[0].APIKeyID)
	}
}

// Usage metadata is bounded on purpose: a polling agent must not cost a write
// per request. Only staleness or a new source IP triggers one.
func TestTouchAccessKeyThrottlesUsageWrites(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	key, _ := mustCreateKey(t, s, userID, serverID)

	read := func() (*time.Time, *string) {
		t.Helper()
		k, err := s.GetAccessKey(ctx, key.ID, userID)
		if err != nil {
			t.Fatal(err)
		}
		return k.LastUsedAt, k.LastUsedIP
	}

	// First use writes.
	if err := s.TouchAccessKey(ctx, key, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	firstAt, firstIP := read()
	if firstAt == nil || firstIP == nil || *firstIP != "203.0.113.7" {
		t.Fatalf("first use not recorded: %v / %v", firstAt, firstIP)
	}

	// A fresh key struct (as authentication produces) from the same IP, moments
	// later, must not write again.
	fresh, err := s.GetAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TouchAccessKey(ctx, fresh, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	secondAt, _ := read()
	if !secondAt.Equal(*firstAt) {
		t.Fatal("a repeat poll from the same IP wrote usage metadata again")
	}

	// A different IP is worth recording immediately — it is the security-relevant
	// change, not the timestamp.
	if err := s.TouchAccessKey(ctx, fresh, "198.51.100.4"); err != nil {
		t.Fatal(err)
	}
	_, movedIP := read()
	if movedIP == nil || *movedIP != "198.51.100.4" {
		t.Fatalf("a new source IP should be recorded, got %v", movedIP)
	}

	// So is a genuinely stale timestamp. Age the stored row (the timestamps this
	// driver round-trips are second-precision, so "old" has to be explicit
	// rather than inferred from how long the test took).
	old := time.Now().Add(-2 * time.Hour).UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, old, key.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := s.GetAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TouchAccessKey(ctx, stale, "198.51.100.4"); err != nil {
		t.Fatal(err)
	}
	refreshedAt, _ := read()
	if refreshedAt == nil || !refreshedAt.After(old.Add(time.Hour)) {
		t.Fatalf("a stale timestamp should be refreshed, got %v", refreshedAt)
	}
}

func TestGenerateAccessKeyTokenIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		token, hash, prefix, err := GenerateAccessKeyToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatal("generated a duplicate token")
		}
		seen[token] = true
		if hash != HashAccessKey(token) || !strings.HasPrefix(token, prefix) {
			t.Fatalf("token/hash/prefix are inconsistent: %q %q %q", token, hash, prefix)
		}
	}
}

// Rotation and revocation race against live traffic in production. The
// invariant is one-directional: once the swap commits, the old secret is dead
// for every subsequent request, and no concurrent reader can observe a state
// where both secrets work.
func TestRotationAndRevocationRaceAgainstAuthentication(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	key, oldToken := mustCreateKey(t, s, userID, serverID)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Readers hammer authentication while the lifecycle changes underneath them.
	// A transient store error is acceptable here; silently accepting a dead
	// secret is not, so the assertion below is on the settled state.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = s.AuthenticateAccessKey(ctx, oldToken)
				}
			}
		}()
	}

	_, newToken, err := s.RotateAccessKey(ctx, key.ID, userID)
	if err != nil {
		close(stop)
		wg.Wait()
		t.Fatal(err)
	}
	// From here the old secret must never authenticate again.
	for i := 0; i < 50; i++ {
		if _, err := s.AuthenticateAccessKey(ctx, oldToken); !errors.Is(err, ErrAccessKeyInvalid) {
			close(stop)
			wg.Wait()
			t.Fatalf("the rotated-away secret authenticated after the swap committed: %v", err)
		}
	}

	if err := s.RevokeAccessKey(ctx, key.ID, userID); err != nil {
		close(stop)
		wg.Wait()
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := s.AuthenticateAccessKey(ctx, newToken); !errors.Is(err, ErrAccessKeyInvalid) {
			close(stop)
			wg.Wait()
			t.Fatalf("a revoked key authenticated: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}

// A WebSocket opened with an access key outlives the request that opened it,
// so it re-checks the exact secret it presented. Rotation and revocation must
// both close it; the key id alone surviving a rotation is not enough.
func TestAccessKeyStillValidTracksRotationAndRevocation(t *testing.T) {
	ctx := context.Background()
	s, userID, serverID := keyFixture(t)
	key, token := mustCreateKey(t, s, userID, serverID)
	hash := HashAccessKey(token)

	if !s.AccessKeyStillValid(ctx, key.ID, hash) {
		t.Fatal("a fresh key does not read as valid")
	}
	if s.AccessKeyStillValid(ctx, key.ID, HashAccessKey(token+"x")) {
		t.Fatal("a different secret read as valid")
	}

	_, rotated, err := s.RotateAccessKey(ctx, key.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if s.AccessKeyStillValid(ctx, key.ID, hash) {
		t.Fatal("the pre-rotation secret still reads as valid")
	}
	rotatedHash := HashAccessKey(rotated)
	if !s.AccessKeyStillValid(ctx, key.ID, rotatedHash) {
		t.Fatal("the rotated secret does not read as valid")
	}

	if err := s.RevokeAccessKey(ctx, key.ID, userID); err != nil {
		t.Fatal(err)
	}
	if s.AccessKeyStillValid(ctx, key.ID, rotatedHash) {
		t.Fatal("a revoked key still reads as valid")
	}
}
