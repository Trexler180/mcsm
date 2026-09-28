package store

import (
	"context"
	"errors"
	"testing"
)

func boolp(b bool) *bool { return &b }

// The whole point of a settings row that may not exist is that its absence is
// unambiguous. A user who has never opened the page must get the gate 027
// shipped with, not an empty struct that happens to zero-value into "no
// password required".
func TestApprovalPolicyDefaultsToSecureWhenUnset(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID, string(MCPScopeActionsRequest))

	policy, err := s.ResolveApprovalPolicy(ctx, ownerID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := SecureApprovalPolicy(); policy != want {
		t.Fatalf("unconfigured policy = %+v, want %+v", policy, want)
	}
}

// A grant override is the more specific statement and must win in both
// directions — including the direction that tightens, so a connection can be
// held to a step-up the account default has dropped.
func TestGrantOverridesWinOverAccountDefaults(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID, string(MCPScopeActionsRequest))

	if err := s.SetUserApprovalSettings(ctx, ownerID, ApprovalPolicy{
		RequirePassword:      false,
		AutoApproveLifecycle: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Inheriting: the account default applies as-is.
	policy, err := s.ResolveApprovalPolicy(ctx, ownerID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.RequirePassword || !policy.AutoApproveLifecycle {
		t.Fatalf("inherited policy = %+v, want the account default", policy)
	}

	// Overriding in the tightening direction.
	if err := s.SetGrantPolicyOverrides(ctx, grant.ID, ownerID, GrantPolicyOverrides{
		RequirePassword:      boolp(true),
		AutoApproveLifecycle: boolp(false),
	}); err != nil {
		t.Fatal(err)
	}
	policy, err = s.ResolveApprovalPolicy(ctx, ownerID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.RequirePassword || policy.AutoApproveLifecycle {
		t.Fatalf("overridden policy = %+v, want the grant's stricter values", policy)
	}

	// Clearing the override returns to inheriting, rather than freezing the
	// value that was last in force.
	if err := s.SetGrantPolicyOverrides(ctx, grant.ID, ownerID, GrantPolicyOverrides{}); err != nil {
		t.Fatal(err)
	}
	policy, err = s.ResolveApprovalPolicy(ctx, ownerID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.RequirePassword || !policy.AutoApproveLifecycle {
		t.Fatalf("policy after clearing overrides = %+v, want the account default back", policy)
	}
}

// A grant id alone must never be enough to retarget somebody else's connection.
func TestSetGrantPolicyOverridesIsScopedToTheOwner(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID, string(MCPScopeActionsRequest))

	other, err := s.CreateUser(ctx, "other@example.com", "pw", "admin")
	if err != nil {
		t.Fatal(err)
	}
	err = s.SetGrantPolicyOverrides(ctx, grant.ID, other.ID, GrantPolicyOverrides{
		AutoApproveUpgrades: boolp(true),
	})
	if !errors.Is(err, ErrMCPGrantNotFound) {
		t.Fatalf("want ErrMCPGrantNotFound, got %v", err)
	}
	policy, err := s.ResolveApprovalPolicy(ctx, ownerID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.AutoApproveUpgrades {
		t.Fatal("another user's write reached the grant")
	}
}

// Upgrades are gated on their own flag. The lifecycle toggle says "let it
// restart the server"; it must never be read as "let it reinstall the runtime
// and roll the world back".
func TestAutoApprovesSeparatesUpgradesFromLifecycle(t *testing.T) {
	lifecycleOnly := ApprovalPolicy{AutoApproveLifecycle: true}
	for _, action := range []string{"start", "stop", "restart"} {
		if !lifecycleOnly.AutoApproves(action) {
			t.Errorf("lifecycle toggle should auto-approve %q", action)
		}
	}
	if lifecycleOnly.AutoApproves("upgrade:26.1.2") {
		t.Error("the lifecycle toggle must not auto-approve a version upgrade")
	}

	upgradesOnly := ApprovalPolicy{AutoApproveUpgrades: true}
	if !upgradesOnly.AutoApproves("upgrade:26.1.2") {
		t.Error("the upgrade toggle should auto-approve an upgrade")
	}
	if upgradesOnly.AutoApproves("restart") {
		t.Error("the upgrade toggle must not auto-approve a restart")
	}
}

// Relaxes decides which edits demand a step-up, so it has to be right about
// every direction rather than just the obvious one.
func TestRelaxesDetectsEveryWeakening(t *testing.T) {
	secure := SecureApprovalPolicy()
	for name, next := range map[string]ApprovalPolicy{
		"dropping the step-up":      {RequirePassword: false},
		"auto-approving lifecycle":  {RequirePassword: true, AutoApproveLifecycle: true},
		"auto-approving upgrades":   {RequirePassword: true, AutoApproveUpgrades: true},
		"dropping and auto-running": {RequirePassword: false, AutoApproveUpgrades: true},
	} {
		if !secure.Relaxes(next) {
			t.Errorf("%s should count as relaxing", name)
		}
	}

	// Tightening, and no-ops, must not demand one — friction on turning a
	// control back on is friction in the wrong direction.
	loose := ApprovalPolicy{RequirePassword: false, AutoApproveLifecycle: true, AutoApproveUpgrades: true}
	if loose.Relaxes(secure) {
		t.Error("returning to the secure policy should not count as relaxing")
	}
	if secure.Relaxes(secure) || loose.Relaxes(loose) {
		t.Error("an unchanged policy should not count as relaxing")
	}
}
