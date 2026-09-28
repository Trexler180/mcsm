package store

import (
	"context"
	"testing"
	"time"
)

// The sweep removes what can no longer authorize anything, and nothing else.
// The second half of that is the part worth a test: the OAuth tables are linked
// by cascading foreign keys, so a careless DELETE on mcp_clients would take live
// delegations with it.
func TestPurgeLeavesLiveDelegationsAlone(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	// Age the client past every retention window. It still has a grant, so it
	// must survive regardless.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_clients SET created_at = ? WHERE client_id = ?`,
		time.Now().UTC().Add(-365*24*time.Hour), client.ClientID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PurgeExpiredMCPRecords(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetMCPClient(ctx, client.ClientID); err != nil {
		t.Fatalf("a client with a live grant was purged: %v", err)
	}
	reloaded, err := s.getMCPGrant(ctx, grant.ID)
	if err != nil {
		t.Fatalf("a live grant was purged: %v", err)
	}
	if !reloaded.Active(time.Now()) {
		t.Fatal("a live grant stopped being active after a sweep")
	}
}

// A registration nobody ever completed is the row this exists for: the endpoint
// is open to the internet, so without a sweep the table only grows.
func TestPurgeRemovesAbandonedClientsAndLapsedRequests(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := mcpFixture(t)

	abandoned, err := s.RegisterMCPClient(ctx, "Drive-by", []string{"http://127.0.0.1:9999/cb"}, "dynamic", "")
	if err != nil {
		t.Fatal(err)
	}
	req, err := s.CreateMCPAuthorizationRequest(ctx, &MCPAuthorizationRequest{
		ClientID: abandoned.ClientID, RedirectURI: "http://127.0.0.1:9999/cb",
		CodeChallenge: "c", CodeChallengeMethod: "S256",
		Scopes: []string{string(MCPScopeServersRead)}, Resource: testResource,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Nothing is due yet, so a sweep now must be a no-op for both rows.
	if _, err := s.PurgeExpiredMCPRecords(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPAuthorizationRequest(ctx, req.ID); err != nil {
		t.Fatalf("a request inside its window was purged: %v", err)
	}

	// Age both past retention and sweep again.
	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_authorization_requests SET expires_at = ? WHERE id = ?`, old, req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_clients SET created_at = ? WHERE client_id = ?`, old, abandoned.ClientID); err != nil {
		t.Fatal(err)
	}

	report, err := s.PurgeExpiredMCPRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests == 0 {
		t.Error("a lapsed authorization request survived the sweep")
	}
	if _, err := s.GetMCPAuthorizationRequest(ctx, req.ID); err == nil {
		t.Error("a lapsed authorization request is still readable")
	}
	if _, err := s.GetMCPClient(ctx, abandoned.ClientID); err == nil {
		t.Error("a client with nothing attached to it survived the sweep")
	}
	if report.Total() == 0 {
		t.Error("the report claims the sweep removed nothing")
	}
}
