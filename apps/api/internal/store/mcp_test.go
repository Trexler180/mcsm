package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// mcpFixture builds the minimum world an MCP grant needs: an owner, a server
// they can see, and a registered client.
func mcpFixture(t *testing.T) (*Store, string, string, *MCPClient) {
	t.Helper()
	ctx := context.Background()
	s, ownerID, serverID := keyFixture(t)
	client, err := s.RegisterMCPClient(ctx, "Claude Code",
		[]string{"http://127.0.0.1:9876/callback"}, "dynamic", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	return s, ownerID, serverID, client
}

const testResource = "https://panel.example.com/api/v1/mcp"

// mustGrant runs a request through to an approved grant plus its code.
func mustGrant(t *testing.T, s *Store, client *MCPClient, ownerID, serverID string, scopes ...string) (*MCPGrant, string) {
	t.Helper()
	ctx := context.Background()
	if len(scopes) == 0 {
		scopes = []string{string(MCPScopeServersRead)}
	}
	req, err := s.CreateMCPAuthorizationRequest(ctx, &MCPAuthorizationRequest{
		ClientID:            client.ClientID,
		RedirectURI:         client.RedirectURIs[0],
		State:               "xyz",
		CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		CodeChallengeMethod: "S256",
		Scopes:              scopes,
		Resource:            testResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, code, err := s.ApproveMCPAuthorization(ctx, req.ID, ownerID, scopes,
		[]string{serverID}, time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return grant, code
}

// An authorization code is a bearer credential that travels through a browser
// redirect, so it must be single-use — and a second presentation is evidence it
// leaked, not a retry to be tolerated.
func TestAuthorizationCodeIsSingleUseAndReplayRevokesTheGrant(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, code := mustGrant(t, s, client, ownerID, serverID)

	if _, _, err := s.RedeemMCPAuthorizationCode(ctx, code); err != nil {
		t.Fatalf("first redemption failed: %v", err)
	}

	_, _, err := s.RedeemMCPAuthorizationCode(ctx, code)
	if !errors.Is(err, ErrMCPCodeReplayed) {
		t.Fatalf("replayed code: want ErrMCPCodeReplayed, got %v", err)
	}

	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Active(time.Now()) {
		t.Fatal("replaying a code must revoke the grant it belonged to")
	}
}

// Two clients racing on the same code must not both get a token.
func TestAuthorizationCodeSurvivesConcurrentRedemption(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	_, code := mustGrant(t, s, client, ownerID, serverID)

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			if _, _, err := s.RedeemMCPAuthorizationCode(ctx, code); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("a code must be redeemable exactly once, got %d successes", wins)
	}
}

// Approving the same parked request twice must not mint two grants.
func TestAuthorizationRequestResolvesOnce(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	scopes := []string{string(MCPScopeServersRead)}
	req, err := s.CreateMCPAuthorizationRequest(ctx, &MCPAuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: "c", CodeChallengeMethod: "S256",
		Scopes: scopes, Resource: testResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(24 * time.Hour)
	if _, _, err := s.ApproveMCPAuthorization(ctx, req.ID, ownerID, scopes, []string{serverID}, expiry); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ApproveMCPAuthorization(ctx, req.ID, ownerID, scopes, []string{serverID}, expiry)
	if !errors.Is(err, ErrMCPRequestResolved) {
		t.Fatalf("second approval: want ErrMCPRequestResolved, got %v", err)
	}
	// Denying an already-resolved request is the same conflict.
	if err := s.DenyMCPAuthorization(ctx, req.ID); !errors.Is(err, ErrMCPRequestResolved) {
		t.Fatalf("deny after approve: want ErrMCPRequestResolved, got %v", err)
	}
}

// An expired parked request cannot become a grant however it is approved.
func TestExpiredAuthorizationRequestCannotBeApproved(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	req, err := s.CreateMCPAuthorizationRequest(ctx, &MCPAuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: "c", CodeChallengeMethod: "S256",
		Scopes: []string{string(MCPScopeServersRead)}, Resource: testResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_authorization_requests SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).UTC(), req.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ApproveMCPAuthorization(ctx, req.ID, ownerID,
		[]string{string(MCPScopeServersRead)}, []string{serverID}, time.Now().Add(time.Hour))
	if !errors.Is(err, ErrMCPRequestExpired) {
		t.Fatalf("want ErrMCPRequestExpired, got %v", err)
	}
}

// Tokens are bearer credentials: only the hash may reach the database, and the
// prefixes must keep the credential families distinguishable.
func TestTokensAreHashedAtRestAndPrefixed(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pair.AccessToken, MCPAccessPrefix) {
		t.Fatalf("access token lacks its prefix: %q", pair.AccessToken)
	}
	if !strings.HasPrefix(pair.RefreshToken, MCPRefreshPrefix) {
		t.Fatalf("refresh token lacks its prefix: %q", pair.RefreshToken)
	}
	// The two families must never be confusable at a boundary.
	for _, prefix := range []string{MCPAccessPrefix, MCPRefreshPrefix, MCPCodePrefix} {
		if strings.HasPrefix(prefix, "mcsm_pat_") || strings.HasPrefix("mcsm_pat_", prefix) {
			t.Fatalf("prefix %q overlaps the access-key prefix", prefix)
		}
	}

	var rows int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE token_hash IN (?,?)`,
		pair.AccessToken, pair.RefreshToken).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("a raw token reached the database")
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE token_hash = ?`,
		HashMCPToken(pair.AccessToken)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("access token should be stored by hash, found %d rows", rows)
	}
}

// A token authorizes nothing outside the exact resource it was minted for.
func TestAccessTokenIsAudienceBound(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource); err != nil {
		t.Fatalf("token should authenticate against its own resource: %v", err)
	}
	for _, wrong := range []string{
		"https://other.example.com/api/v1/mcp",
		"https://panel.example.com/api/v1/mcp/extra",
		"https://panel.example.com",
		"",
	} {
		if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, wrong); !errors.Is(err, ErrMCPTokenInvalid) {
			t.Fatalf("resource %q: want ErrMCPTokenInvalid, got %v", wrong, err)
		}
	}
}

// Every rejection must be indistinguishable, so a client cannot learn whether a
// token was unknown, expired, revoked, or orphaned.
func TestAccessTokenRejectionsAreUniform(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)

	// Unknown.
	if _, err := s.AuthenticateMCPAccessToken(ctx, MCPAccessPrefix+"nope", testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("unknown token: %v", err)
	}
	// Wrong family (a refresh token presented as an access token).
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.RefreshToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("refresh token presented as access token: %v", err)
	}

	// Expired.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_tokens SET expires_at = ? WHERE token_hash = ?`,
		time.Now().Add(-time.Second).UTC(), HashMCPToken(pair.AccessToken)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("expired token: %v", err)
	}

	// Revoked grant.
	grant2, _ := mustGrant(t, s, client, ownerID, serverID)
	pair2, err := s.IssueMCPTokens(ctx, grant2, grant2.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeMCPGrant(ctx, grant2.ID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair2.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("revoked grant: %v", err)
	}
}

// Deleting the owner must strand every delegation they made. A grant acting as
// nobody would otherwise keep its authority after the account is gone.
func TestOwnerDeletionInvalidatesTokens(t *testing.T) {
	ctx := context.Background()
	s, _, serverID, client := mcpFixture(t)

	// A collaborator rather than the server's owner, so the account can be
	// removed the way a departing team member's would be.
	collaborator, err := s.CreateUser(ctx, "collab@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := mustGrant(t, s, client, collaborator.ID, serverID)
	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource); err != nil {
		t.Fatalf("the token should work while its owner exists: %v", err)
	}

	if err := s.DeleteUser(ctx, collaborator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("token outlived its owner: %v", err)
	}
}

// Narrowing a grant must narrow tokens already issued under it, because a token
// carries a copy of the scopes and the grant is the authority.
func TestGrantIsAuthoritativeOverIssuedTokenScopes(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID,
		string(MCPScopeServersRead), string(MCPScopeActionsRequest))
	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	// Narrow the grant behind the token's back.
	if _, err := s.db.ExecContext(ctx, `UPDATE mcp_grants SET scopes = ? WHERE id = ?`,
		strArray([]string{string(MCPScopeServersRead)}), grant.ID); err != nil {
		t.Fatal(err)
	}
	principal, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource)
	if err != nil {
		t.Fatal(err)
	}
	if HasMCPScope(principal.Scopes, MCPScopeActionsRequest) {
		t.Fatal("a token kept a capability the grant no longer carries")
	}
	if !HasMCPScope(principal.Scopes, MCPScopeServersRead) {
		t.Fatal("the token lost a capability the grant still carries")
	}
}

// A grant row whose scope column has been corrupted must fail closed rather
// than authorize an unrecognized capability.
func TestUnparsableGrantScopesFailClosed(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	pair, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mcp_grants SET scopes = ? WHERE id = ?`,
		strArray([]string{"mcp:everything"}), grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pair.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("unknown stored scope must fail closed, got %v", err)
	}
}

// Refresh rotation replaces the pair and retires the old access token.
func TestRefreshRotationSupersedesTheOldPair(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	_, second, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}
	if second.AccessToken == first.AccessToken || second.RefreshToken == first.RefreshToken {
		t.Fatal("rotation must mint new material")
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, first.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatal("the superseded access token still works")
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, second.AccessToken, testResource); err != nil {
		t.Fatalf("the new access token should work: %v", err)
	}
}

// Presenting a refresh token twice means it leaked. The only safe reading is
// that the delegation is compromised, so the whole family goes.
func TestRefreshReplayRevokesTheFamilyAndGrant(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
	if err != nil {
		t.Fatal(err)
	}
	backdateRefreshUse(t, s, first.RefreshToken, MCPRefreshReuseGrace+time.Second)

	if _, _, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource); !errors.Is(err, ErrMCPRefreshReplayed) {
		t.Fatalf("replay: want ErrMCPRefreshReplayed, got %v", err)
	}
	// The legitimate client's fresh token dies too — that is the point: after a
	// leak, both parties must re-authorize rather than race.
	if _, err := s.AuthenticateMCPAccessToken(ctx, second.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatal("the family survived a replay")
	}
	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Active(time.Now()) {
		t.Fatal("a replayed refresh token must revoke the grant")
	}
}

// A refresh request naming the wrong client must not touch the token. These
// clients are public, so anyone who observes a refresh token could otherwise
// send it with a bogus client_id and retire a delegation they do not own: the
// rotation would consume the token, the response would be discarded, and the
// legitimate client would be locked out.
func TestRefreshRotationRejectsAWrongClientWithoutConsumingTheToken(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, "mcp_client_wrong", testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("wrong client id: want ErrMCPTokenInvalid, got %v", err)
	}
	// An absent client_id is the same refusal: there is nothing to bind against.
	if _, _, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, "", testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("missing client id: want ErrMCPTokenInvalid, got %v", err)
	}

	// The token is untouched: neither consumed nor marked used, and the grant
	// behind it is still live.
	var usedAt, revokedAt *time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT used_at, revoked_at FROM mcp_tokens WHERE token_hash = ?`,
		HashMCPToken(first.RefreshToken)).Scan(&usedAt, &revokedAt); err != nil {
		t.Fatal(err)
	}
	if usedAt != nil || revokedAt != nil {
		t.Fatalf("a rejected refresh consumed the token (used_at=%v revoked_at=%v)", usedAt, revokedAt)
	}
	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Active(time.Now()) {
		t.Fatal("a rejected refresh revoked the grant")
	}

	// And the legitimate client can still rotate the very same token.
	_, second, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
	if err != nil {
		t.Fatalf("the real client must still be able to rotate: %v", err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, second.AccessToken, testResource); err != nil {
		t.Fatalf("the rotated access token should work: %v", err)
	}
}

// backdateRefreshUse moves a consumed refresh token's used_at into the past, so
// a test can present it again outside (or inside) the reuse grace without
// sleeping.
func backdateRefreshUse(t *testing.T, s *Store, refresh string, ago time.Duration) {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`UPDATE mcp_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NOT NULL`,
		time.Now().Add(-ago).UTC(), HashMCPToken(refresh))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("backdating refresh use touched %d rows, want 1", n)
	}
}

// A retry after a lost token response, or a second session sharing the stored
// credential, presents the rotated token again moments later. That is not a
// leak: it gets a working sibling pair, and the pair the first presentation
// received keeps working too.
func TestRefreshReuseWithinGraceIssuesASiblingPair(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
	if err != nil {
		t.Fatal(err)
	}
	backdateRefreshUse(t, s, first.RefreshToken, MCPRefreshReuseGrace-5*time.Second)

	_, sibling, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
	if err != nil {
		t.Fatalf("a reuse inside the grace must succeed: %v", err)
	}
	if sibling.AccessToken == second.AccessToken || sibling.RefreshToken == second.RefreshToken {
		t.Fatal("the sibling pair must be new material")
	}
	for name, token := range map[string]string{"first rotation": second.AccessToken, "sibling": sibling.AccessToken} {
		if _, err := s.AuthenticateMCPAccessToken(ctx, token, testResource); err != nil {
			t.Fatalf("%s access token should work: %v", name, err)
		}
	}
	// Both chains stay refreshable.
	if _, _, err := s.RotateMCPRefreshToken(ctx, second.RefreshToken, client.ClientID, testResource); err != nil {
		t.Fatalf("the first chain must still rotate: %v", err)
	}
	if _, _, err := s.RotateMCPRefreshToken(ctx, sibling.RefreshToken, client.ClientID, testResource); err != nil {
		t.Fatalf("the sibling chain must still rotate: %v", err)
	}
	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Active(time.Now()) {
		t.Fatal("a reuse inside the grace must not revoke the grant")
	}
}

// Concurrent presentations of one refresh token are a client racing itself.
// Exactly one consumes the token; the rest are settled as reuses inside the
// grace, and the delegation survives.
func TestConcurrentRefreshRotationConsumesOnceAndKeepsTheGrant(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	const racers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var pairs []*MCPTokenPair
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			_, pair, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource)
			if errors.Is(err, ErrMCPRefreshReplayed) {
				t.Errorf("a concurrent presentation was treated as a replay")
			}
			if err == nil {
				mu.Lock()
				pairs = append(pairs, pair)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(pairs) == 0 {
		t.Fatal("no presentation succeeded")
	}

	var consumed int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE token_hash = ? AND used_at IS NOT NULL`,
		HashMCPToken(first.RefreshToken)).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != 1 {
		t.Fatalf("the refresh token must be consumed exactly once, got %d", consumed)
	}
	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Active(time.Now()) {
		t.Fatal("concurrent presentations must not revoke the grant")
	}
}

// A rotation that cannot complete leaves the presented token unconsumed and
// the family untouched, so the client's retry is a plain rotation rather than
// a reuse or a replay.
func TestFailedRotationDoesNotConsumeTheToken(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)
	first, err := s.IssueMCPTokens(ctx, grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	// Narrow the grant to nothing this chain holds: issuance fails after the
	// consuming UPDATE has already run inside the transaction.
	if _, err := s.db.ExecContext(ctx, `UPDATE mcp_grants SET scopes = ? WHERE id = ?`,
		strArray([]string{string(MCPScopeLogsRead)}), grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateMCPRefreshToken(ctx, first.RefreshToken, client.ClientID, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatalf("want ErrMCPTokenInvalid, got %v", err)
	}

	var refreshUsed *time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT used_at FROM mcp_tokens WHERE token_hash = ?`,
		HashMCPToken(first.RefreshToken)).Scan(&refreshUsed); err != nil {
		t.Fatal(err)
	}
	if refreshUsed != nil {
		t.Fatal("a failed rotation consumed the refresh token")
	}
	var accessRevoked *time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT revoked_at FROM mcp_tokens WHERE token_hash = ?`,
		HashMCPToken(first.AccessToken)).Scan(&accessRevoked); err != nil {
		t.Fatal(err)
	}
	if accessRevoked != nil {
		t.Fatal("a failed rotation revoked the family's access token")
	}
}

// A refresh token must never outlive the grant behind it.
func TestTokenLifetimesAreClampedToTheGrant(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	scopes := []string{string(MCPScopeServersRead)}
	req, err := s.CreateMCPAuthorizationRequest(ctx, &MCPAuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: "c", CodeChallengeMethod: "S256",
		Scopes: scopes, Resource: testResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A grant that expires in five minutes — well inside the refresh lifetime.
	grantExpiry := time.Now().Add(5 * time.Minute)
	grant, _, err := s.ApproveMCPAuthorization(ctx, req.ID, ownerID, scopes, []string{serverID}, grantExpiry)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := s.IssueMCPTokens(ctx, grant, scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	var refreshExpiry time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM mcp_tokens WHERE token_hash = ?`,
		HashMCPToken(pair.RefreshToken)).Scan(&refreshExpiry); err != nil {
		t.Fatal(err)
	}
	if refreshExpiry.After(grant.ExpiresAt.Add(time.Second)) {
		t.Fatalf("refresh token (%s) outlives its grant (%s)", refreshExpiry, grant.ExpiresAt)
	}
}

// RFC 7009: revoking a refresh token retires the delegation; revoking an access
// token retires only that token. Both answer without reporting existence.
func TestRevocationByTokenValue(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)

	// Access token: only that token dies, so a refresh can still recover.
	grantA, _ := mustGrant(t, s, client, ownerID, serverID)
	pairA, err := s.IssueMCPTokens(ctx, grantA, grantA.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeMCPTokenByValue(ctx, pairA.AccessToken, client.ClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateMCPAccessToken(ctx, pairA.AccessToken, testResource); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatal("the revoked access token still works")
	}
	if _, _, err := s.RotateMCPRefreshToken(ctx, pairA.RefreshToken, client.ClientID, testResource); err != nil {
		t.Fatalf("refreshing after access-token revocation should recover: %v", err)
	}

	// Refresh token: the whole delegation goes.
	grantB, _ := mustGrant(t, s, client, ownerID, serverID)
	pairB, err := s.IssueMCPTokens(ctx, grantB, grantB.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	// Named by the wrong client, or by none, nothing happens.
	for _, wrong := range []string{"mcp_client_wrong", ""} {
		if err := s.RevokeMCPTokenByValue(ctx, pairB.RefreshToken, wrong); err != nil {
			t.Fatal(err)
		}
		if g, err := s.getMCPGrant(ctx, grantB.ID); err != nil || !g.Active(time.Now()) {
			t.Fatalf("revocation naming client %q retired the grant", wrong)
		}
	}
	if err := s.RevokeMCPTokenByValue(ctx, pairB.RefreshToken, client.ClientID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.getMCPGrant(ctx, grantB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Active(time.Now()) {
		t.Fatal("revoking a refresh token must retire the grant")
	}

	// An unknown token is a silent success, never an oracle.
	if err := s.RevokeMCPTokenByValue(ctx, MCPAccessPrefix+"never-existed", client.ClientID); err != nil {
		t.Fatalf("revoking an unknown token must not error: %v", err)
	}
}

// One person's grant id must be useless to another.
func TestGrantsAreScopedToTheirOwner(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	other, err := s.CreateUser(ctx, "other@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPGrantForUser(ctx, grant.ID, other.ID); !errors.Is(err, ErrMCPGrantNotFound) {
		t.Fatalf("want ErrMCPGrantNotFound, got %v", err)
	}
	if err := s.RevokeMCPGrant(ctx, grant.ID, other.ID); !errors.Is(err, ErrMCPGrantNotFound) {
		t.Fatalf("revoking someone else's grant: want ErrMCPGrantNotFound, got %v", err)
	}
	// ...and it is still live for its actual owner.
	if reloaded, err := s.getMCPGrant(ctx, grant.ID); err != nil || !reloaded.Active(time.Now()) {
		t.Fatal("a failed cross-owner revocation must not affect the grant")
	}
}

// A grant with no scopes or no servers authorizes nothing by construction.
func TestEmptyGrantIsInert(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	cases := []struct {
		name  string
		grant MCPGrant
	}{
		{"no scopes", MCPGrant{ExpiresAt: future, ServerIDs: []string{"s"}}},
		{"no servers", MCPGrant{ExpiresAt: future, Scopes: []string{"mcp:servers.read"}}},
		{"expired", MCPGrant{ExpiresAt: now.Add(-time.Second), Scopes: []string{"x"}, ServerIDs: []string{"s"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.grant.Active(now) {
				t.Fatal("grant should be inert")
			}
		})
	}
	if (&MCPGrant{}).AllowsServer("s") {
		t.Fatal("an empty allowlist must allow nothing")
	}
	if (&MCPGrant{ServerIDs: []string{"a"}}).AllowsServer("") {
		t.Fatal("an empty server id must never match")
	}
}

// Redirect matching is exact. Every near-miss below is a way an authorization
// code could otherwise be delivered somewhere it was not registered for.
func TestRedirectMatchingIsExact(t *testing.T) {
	client := &MCPClient{RedirectURIs: []string{"http://127.0.0.1:9876/callback"}}
	if !client.AllowsRedirect("http://127.0.0.1:9876/callback") {
		t.Fatal("the registered URI must match itself")
	}
	for _, near := range []string{
		"http://127.0.0.1:9876/callback/",
		"http://127.0.0.1:9876/callback?x=1",
		"http://127.0.0.1:9876/Callback",
		"http://127.0.0.1:9877/callback",
		"https://127.0.0.1:9876/callback",
		"http://127.0.0.1:9876/callback/../evil",
		"http://evil.example.com/callback",
		"",
	} {
		if client.AllowsRedirect(near) {
			t.Fatalf("%q must not match the registered redirect URI", near)
		}
	}
	if (*MCPClient)(nil).AllowsRedirect("anything") {
		t.Fatal("a nil client must allow nothing")
	}
}

// The scope vocabulary is closed: an unknown scope is an error, never a silent
// drop that would leave a client believing it holds more than it does.
func TestScopeNormalizationIsClosed(t *testing.T) {
	if _, err := NormalizeMCPScopes([]string{"mcp:servers.read", "mcp:root"}); !errors.Is(err, ErrMCPUnknownScope) {
		t.Fatalf("want ErrMCPUnknownScope, got %v", err)
	}
	if _, err := NormalizeMCPScopes(nil); !errors.Is(err, ErrMCPNoScopes) {
		t.Fatalf("want ErrMCPNoScopes, got %v", err)
	}
	got, err := NormalizeMCPScopes([]string{"mcp:logs.read", "mcp:servers.read", "mcp:logs.read", " "})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp:logs.read", "mcp:servers.read"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
	// Every advertised scope must map to a live permission requirement, or the
	// consent screen would offer something nothing enforces.
	for _, scope := range AllMCPScopes() {
		if _, _, ok := MCPScopeRequirement(scope); !ok {
			t.Fatalf("advertised scope %q has no permission requirement", scope)
		}
	}
	// There is no hierarchy and no wildcard.
	if HasMCPScope([]string{"mcp:servers.read"}, MCPScopeActionsRequest) {
		t.Fatal("scopes must not subsume one another")
	}
	if HasMCPScope([]string{"*"}, MCPScopeServersRead) {
		t.Fatal("a wildcard must not satisfy a scope")
	}
}

// Only start, stop, and restart are requestable. Everything else the panel can
// do must be unreachable from a delegation.
func TestOnlyLifecycleActionsAreRequestable(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		if _, ok := MCPActionPermission(action); !ok {
			t.Fatalf("%q should be requestable", action)
		}
	}
	for _, action := range []string{"kill", "reinstall", "restore", "delete", "command", "", "START"} {
		if _, ok := MCPActionPermission(action); ok {
			t.Fatalf("%q must not be requestable", action)
		}
	}
}

// A self-registering client picks its own display name, so it is untrusted text
// the moment it arrives — and it is stored, then read back by a human deciding
// whether to approve the connection. Bounding it by byte was the bug: cutting a
// multi-byte character in half stores invalid UTF-8.
func TestClientNameIsBoundedOnRuneBoundaries(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	client, err := s.RegisterMCPClient(ctx, strings.Repeat("あ", 400),
		[]string{"http://127.0.0.1:9876/cb"}, "dynamic", strings.Repeat("い", 400))
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(client.ClientName) {
		t.Errorf("stored client name is not valid UTF-8: %q", client.ClientName)
	}
	if got := utf8.RuneCountInString(client.ClientName); got != 100 {
		t.Errorf("client name is %d runes, want 100", got)
	}
	if client.SoftwareID == nil {
		t.Fatal("software id was dropped")
	}
	if !utf8.ValidString(*client.SoftwareID) {
		t.Errorf("stored software id is not valid UTF-8: %q", *client.SoftwareID)
	}
	if got := utf8.RuneCountInString(*client.SoftwareID); got != 100 {
		t.Errorf("software id is %d runes, want 100", got)
	}
}

// A name that is only invisible characters is not a name. It must not become
// one by surviving the trim, because the consent screen would then show a human
// an empty string where the client's identity should be.
func TestBlankClientNameFallsBackToAPlaceholder(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	client, err := s.RegisterMCPClient(ctx, "  ", []string{"http://127.0.0.1:9876/cb"}, "dynamic", "")
	if err != nil {
		t.Fatal(err)
	}
	if client.ClientName != "Unnamed MCP client" {
		t.Errorf("client name = %q", client.ClientName)
	}
}
