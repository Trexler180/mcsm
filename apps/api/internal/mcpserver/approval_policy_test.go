package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

func boolp(b bool) *bool { return &b }

// recordingNotifier captures the alerts the facade raises so a test can assert
// the owner is actually told, rather than only that the row changed.
type recordingNotifier struct {
	events []notify.Event
	users  []string
}

func (r *recordingNotifier) EmitToUser(userID string, evt notify.Event) {
	r.users = append(r.users, userID)
	r.events = append(r.events, evt)
}

func setPolicy(t *testing.T, e *env, p store.ApprovalPolicy) {
	t.Helper()
	if err := e.store.SetUserApprovalSettings(context.Background(), e.ownerID, p); err != nil {
		t.Fatal(err)
	}
}

// The default path must not change: filing a request still parks it for a human
// and touches nothing.
func TestActionRequestStaysPendingWithoutAutoApproval(t *testing.T) {
	e := newEnv(t)
	p := principalFor(e.grant)

	_, out, err := e.svc.requestServerAction(p)(context.Background(), nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionPending {
		t.Fatalf("status = %q, want pending", out.Status)
	}
}

// With the lifecycle toggle on, the request comes back already executed — and
// with no human recorded as having decided it, because none did.
func TestAutoApprovedLifecycleActionRunsAndRecordsNoDecider(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	setPolicy(t, e, store.ApprovalPolicy{RequirePassword: true, AutoApproveLifecycle: true})
	p := principalFor(e.grant)

	_, out, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionExecuted {
		t.Fatalf("status = %q, want executed", out.Status)
	}

	req, err := e.store.GetMCPActionRequest(ctx, out.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if req.DecidedBy != nil {
		t.Fatalf("decided_by = %v, want NULL — nobody looked at this request", *req.DecidedBy)
	}
}

// The audit log must never claim a human approved something no human saw.
func TestAutoApprovalAuditsUnderItsOwnAction(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	setPolicy(t, e, store.ApprovalPolicy{AutoApproveLifecycle: true})
	p := principalFor(e.grant)

	if _, _, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := e.store.ListAudit(ctx, e.serverA, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawAuto, sawHuman bool
	for _, entry := range entries {
		switch entry.Action {
		case "mcp.action.auto_approved":
			sawAuto = true
		case "mcp.action.approved":
			sawHuman = true
		}
	}
	if !sawAuto {
		t.Error("no mcp.action.auto_approved entry was written")
	}
	if sawHuman {
		t.Error("an unattended action was logged as mcp.action.approved")
	}
}

// The two toggles are independent in both directions. This is the mistake worth
// a test: a lifecycle toggle that also let upgrades through would hand an agent
// the ability to reinstall a runtime and roll a world back unattended.
func TestLifecycleToggleDoesNotAutoApproveUpgrades(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fake := &fakeMigrationStarter{}
	e.svc.migrations = fake
	setPolicy(t, e, store.ApprovalPolicy{AutoApproveLifecycle: true})
	p := principalFor(e.grant)

	_, out, err := e.svc.requestVersionUpgrade(p)(ctx, nil, versionUpgradeInput{
		ServerID: e.serverA, TargetVersion: "1.21.5", Reason: "compatibility was reviewed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionPending {
		t.Fatalf("status = %q, want pending — the lifecycle toggle must not cover upgrades", out.Status)
	}
	if fake.calls != 0 {
		t.Fatalf("migration ran %d times without approval", fake.calls)
	}
}

func TestUpgradeToggleAutoApprovesUpgrades(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fake := &fakeMigrationStarter{}
	e.svc.migrations = fake
	setPolicy(t, e, store.ApprovalPolicy{AutoApproveUpgrades: true})
	p := principalFor(e.grant)

	_, out, err := e.svc.requestVersionUpgrade(p)(ctx, nil, versionUpgradeInput{
		ServerID: e.serverA, TargetVersion: "1.21.5", Reason: "compatibility was reviewed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionExecuted {
		t.Fatalf("status = %q, want executed", out.Status)
	}
	if fake.calls != 1 {
		t.Fatalf("migration calls = %d, want 1", fake.calls)
	}
}

// A grant override beats the account default here too, so one connection can be
// trusted without trusting every future one.
func TestGrantOverrideDrivesAutoApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.store.SetGrantPolicyOverrides(ctx, e.grant.ID, e.ownerID,
		store.GrantPolicyOverrides{AutoApproveLifecycle: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	p := principalFor(e.grant)

	_, out, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionExecuted {
		t.Fatalf("status = %q, want executed via the grant override", out.Status)
	}
}

// A pending request has to reach the one person who can answer it, carrying
// enough for the panel to draw the right buttons.
func TestPendingRequestNotifiesTheGrantOwner(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	recorder := &recordingNotifier{}
	e.svc.notifier = recorder

	if _, _, err := e.svc.requestServerAction(principalFor(e.grant))(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "unresponsive",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != notify.EventMCPActionRequested {
		t.Fatalf("pending request raised %+v, want one %s", recorder.events, notify.EventMCPActionRequested)
	}
	if recorder.users[0] != e.ownerID {
		t.Fatalf("notified %q, want the grant owner %q", recorder.users[0], e.ownerID)
	}
	if got := recorder.events[0].Data["requires_password"]; got != true {
		t.Fatalf("requires_password = %v, want true under the default policy", got)
	}
}

// Removing the human from the loop must not also remove their visibility: an
// operator who turned auto-approval on should still find out what their agent
// did without going looking for it.
func TestAutoApprovedRequestStillNotifiesTheOwner(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	setPolicy(t, e, store.ApprovalPolicy{AutoApproveLifecycle: true})
	recorder := &recordingNotifier{}
	e.svc.notifier = recorder

	if _, _, err := e.svc.requestServerAction(principalFor(e.grant))(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "unresponsive",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != notify.EventMCPActionResolved {
		t.Fatalf("auto-approved request raised %+v, want one %s", recorder.events, notify.EventMCPActionResolved)
	}
	if got := recorder.events[0].Data["automatic"]; got != true {
		t.Fatalf("automatic = %v, want true so the operator knows nobody was asked", got)
	}
	if recorder.users[0] != e.ownerID {
		t.Fatalf("notified %q, want the grant owner %q", recorder.users[0], e.ownerID)
	}
}

// A request answered anywhere has to be announced, so a prompt still showing on
// another device comes down instead of offering buttons that can only fail.
func TestDenyingRaisesAResolutionAlert(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	recorder := &recordingNotifier{}
	e.svc.notifier = recorder

	_, filed, err := e.svc.requestServerAction(principalFor(e.grant))(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DenyAction(ctx, filed.RequestID, e.ownerID, e.ownerID, ""); err != nil {
		t.Fatal(err)
	}

	if len(recorder.events) != 2 {
		t.Fatalf("raised %d alerts, want a prompt then a resolution", len(recorder.events))
	}
	resolved := recorder.events[1]
	if resolved.Type != notify.EventMCPActionResolved {
		t.Fatalf("second alert = %s, want %s", resolved.Type, notify.EventMCPActionResolved)
	}
	if got := resolved.Data["request_id"]; got != filed.RequestID {
		t.Fatalf("request_id = %v, want %q — the prompt is keyed on it", got, filed.RequestID)
	}
	// A human decided this one, so it must not be reported as unattended.
	if got := resolved.Data["automatic"]; got != false {
		t.Fatalf("automatic = %v, want false", got)
	}
}

// ── The wait tool ────────────────────────────────────────────────

// The point of the tool: a decision made while the agent is waiting reaches it
// without the human having to re-prompt.
func TestAwaitReturnsAsSoonAsTheRequestIsDecided(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	_, filed, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Decide it from "the dashboard" while the wait is in flight.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = e.svc.DenyAction(context.Background(), filed.RequestID, e.ownerID, e.ownerID, "127.0.0.1")
	}()

	start := time.Now()
	_, out, err := e.svc.awaitActionRequest(p)(ctx, nil, awaitActionInput{RequestID: filed.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionDenied {
		t.Fatalf("status = %q, want denied", out.Status)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("wait took %s; it should return on the decision, not on its deadline", elapsed)
	}
}

// An already-settled request returns immediately rather than waiting out the
// budget for a decision that has already been made.
func TestAwaitReturnsImmediatelyForASettledRequest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	_, filed, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DenyAction(ctx, filed.RequestID, e.ownerID, e.ownerID, ""); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, out, err := e.svc.awaitActionRequest(p)(ctx, nil, awaitActionInput{RequestID: filed.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionDenied {
		t.Fatalf("status = %q, want denied", out.Status)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("settled request took %s to return", elapsed)
	}
}

// A cancelled call must report the request as it stands rather than erroring:
// it is still pending, still valid, and the agent should be told to call again.
func TestAwaitReportsStillPendingWhenTheCallIsCancelled(t *testing.T) {
	e := newEnv(t)
	p := principalFor(e.grant)

	_, filed, err := e.svc.requestServerAction(p)(context.Background(), nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, out, err := e.svc.awaitActionRequest(p)(ctx, nil, awaitActionInput{RequestID: filed.RequestID})
	if err != nil {
		t.Fatalf("a timed-out wait is not an error: %v", err)
	}
	if out.Status != store.MCPActionPending {
		t.Fatalf("status = %q, want pending", out.Status)
	}
	if out.Note != awaitTimedOutNote {
		t.Fatalf("note = %q, want the retry instruction", out.Note)
	}
}

// Waiting is a read, and it is scoped exactly as the read it wraps: one
// delegation can never observe another's queue.
func TestAwaitIsScopedToTheFilingGrant(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, filed, err := e.svc.requestServerAction(principalFor(e.grant))(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "the server is unresponsive",
	})
	if err != nil {
		t.Fatal(err)
	}

	other := principalFor(e.grant)
	other.GrantID = "some-other-grant"
	if _, _, err := e.svc.awaitActionRequest(other)(ctx, nil, awaitActionInput{RequestID: filed.RequestID}); err == nil {
		t.Fatal("another grant could wait on this request")
	}
}
