package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// actionFixture builds an owner, a server, and an active grant that covers it.
func actionFixture(t *testing.T) (*Store, string, string, *MCPGrant) {
	t.Helper()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID,
		string(MCPScopeServersRead), string(MCPScopeActionsRequest))
	return s, ownerID, serverID, grant
}

// Filing a request must not touch a server: it exists so a human can decide.
func TestActionRequestStartsPendingAndBounded(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)

	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "restart", "TPS has been under 5 for ten minutes")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != MCPActionPending {
		t.Fatalf("a new request must be pending, got %q", req.Status)
	}
	if req.ExecutedAt != nil || req.DecidedAt != nil {
		t.Fatal("a new request must not be decided or executed")
	}
	if !req.ExpiresAt.After(time.Now()) {
		t.Fatal("a new request must have a future deadline")
	}
	if req.ExpiresAt.After(time.Now().Add(MCPActionRequestLifetime + time.Minute)) {
		t.Fatal("the deadline must be bounded by MCPActionRequestLifetime")
	}
}

// Only the three lifecycle actions can be filed at all.
func TestUnsupportedActionsCannotBeFiled(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	for _, action := range []string{"kill", "reinstall", "restore", "command", "delete", ""} {
		if _, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, action, "because"); !errors.Is(err, ErrMCPActionUnsupported) {
			t.Fatalf("%q: want ErrMCPActionUnsupported, got %v", action, err)
		}
	}
}

// The model writes the reason, so it is bounded before it is ever stored.
func TestActionReasonIsBounded(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "stop", strings.Repeat("A", 10_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Reason) > mcpActionReasonMax {
		t.Fatalf("reason stored at %d chars, cap is %d", len(req.Reason), mcpActionReasonMax)
	}
}

// The claim is the at-most-once guarantee. Under concurrent approval exactly
// one caller may proceed to touch a node.
func TestClaimIsAtMostOnceUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "restart", "why not")
	if err != nil {
		t.Fatal(err)
	}

	const racers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			if _, err := s.ClaimMCPActionRequest(ctx, req.ID, ownerID, ownerID); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("exactly one caller may claim a request, got %d", wins)
	}
	// Every loser sees the same benign outcome.
	if _, err := s.ClaimMCPActionRequest(ctx, req.ID, ownerID, ownerID); !errors.Is(err, ErrMCPActionNotPending) {
		t.Fatalf("a later claim: want ErrMCPActionNotPending, got %v", err)
	}
}

// A request that lapsed cannot be approved, even by someone who was looking at
// it when it was still live.
func TestExpiredRequestCannotBeClaimed(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "start", "please")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mcp_action_requests SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Second).UTC(), req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPActionRequest(ctx, req.ID, ownerID, ownerID); !errors.Is(err, ErrMCPActionNotPending) {
		t.Fatalf("want ErrMCPActionNotPending, got %v", err)
	}
	// And it reads as expired to a polling agent immediately, without a sweep.
	reloaded, err := s.GetMCPActionRequest(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.EffectiveStatus(time.Now()); got != MCPActionExpired {
		t.Fatalf("want expired, got %q", got)
	}
}

// Denying settles the request, and a denied request can never be executed.
func TestDenyIsTerminal(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "stop", "hunch")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DenyMCPActionRequest(ctx, req.ID, ownerID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPActionRequest(ctx, req.ID, ownerID, ownerID); !errors.Is(err, ErrMCPActionNotPending) {
		t.Fatalf("a denied request must not be claimable, got %v", err)
	}
	if err := s.DenyMCPActionRequest(ctx, req.ID, ownerID, ownerID); !errors.Is(err, ErrMCPActionNotPending) {
		t.Fatalf("denying twice: want ErrMCPActionNotPending, got %v", err)
	}
	settled, err := s.GetMCPActionRequest(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != MCPActionDenied || settled.DecidedAt == nil {
		t.Fatalf("denied request not settled: %+v", settled)
	}
}

// One delegation must never observe another's queue, not even under the same
// owner: a second client should not learn what the first is doing.
func TestActionRequestsAreScopedToTheirGrant(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "restart", "one")
	if err != nil {
		t.Fatal(err)
	}

	client2, err := s.RegisterMCPClient(ctx, "Codex", []string{"http://127.0.0.1:1455/cb"}, "dynamic", "codex")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := mustGrant(t, s, client2, ownerID, serverID, string(MCPScopeActionsRequest))

	if _, err := s.GetMCPActionRequestForGrant(ctx, req.ID, other.ID); !errors.Is(err, ErrMCPActionNotFound) {
		t.Fatalf("a second grant read the first's request: %v", err)
	}
	if _, err := s.GetMCPActionRequestForGrant(ctx, req.ID, grant.ID); err != nil {
		t.Fatalf("the filing grant must be able to observe its own request: %v", err)
	}
}

// Settling a claimed request records success or the panel's own failure text.
func TestFinishSettlesExecutedOrFailed(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)

	executed, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "start", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPActionRequest(ctx, executed.ID, ownerID, ownerID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishMCPActionRequest(ctx, executed.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMCPActionRequest(ctx, executed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != MCPActionExecuted || got.ExecutedAt == nil {
		t.Fatalf("want executed with a timestamp, got %+v", got)
	}

	failed, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "stop", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPActionRequest(ctx, failed.ID, ownerID, ownerID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishMCPActionRequest(ctx, failed.ID, "the node did not accept the stop request"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetMCPActionRequest(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != MCPActionFailed || got.FailureReason == nil {
		t.Fatalf("want failed with a reason, got %+v", got)
	}
	// A settled request is out of pending for good.
	if _, err := s.ClaimMCPActionRequest(ctx, failed.ID, ownerID, ownerID); !errors.Is(err, ErrMCPActionNotPending) {
		t.Fatalf("a failed request must not be re-claimable, got %v", err)
	}
}

// Revoking the grant must not erase the record of what it asked for.
func TestActionHistorySurvivesGrantRevocation(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)
	req, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "restart", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeMCPGrant(ctx, grant.ID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPActionRequest(ctx, req.ID); err != nil {
		t.Fatalf("the request disappeared with its grant: %v", err)
	}
	listed, err := s.ListMCPActionRequestsForUser(ctx, ownerID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != req.ID {
		t.Fatalf("owner should still see the request, got %d rows", len(listed))
	}
	if listed[0].ClientName == "" {
		t.Fatal("the client name should still be joined for display")
	}
}

// The sweep settles forgotten requests without touching decided ones.
func TestExpirySweepOnlyTouchesPending(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, grant := actionFixture(t)

	stale, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "start", "old")
	if err != nil {
		t.Fatal(err)
	}
	decided, err := s.CreateMCPActionRequest(ctx, grant.ID, ownerID, serverID, "stop", "decided")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DenyMCPActionRequest(ctx, decided.ID, ownerID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mcp_action_requests SET expires_at = ?`,
		time.Now().Add(-time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}

	if err := s.ExpireStaleMCPActionRequests(ctx); err != nil {
		t.Fatal(err)
	}
	sweptStale, err := s.GetMCPActionRequest(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sweptStale.Status != MCPActionExpired {
		t.Fatalf("stale pending request not swept: %q", sweptStale.Status)
	}
	sweptDecided, err := s.GetMCPActionRequest(ctx, decided.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sweptDecided.Status != MCPActionDenied {
		t.Fatalf("the sweep overwrote a decision: %q", sweptDecided.Status)
	}
}
