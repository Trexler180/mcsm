package mcpserver

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// A self-registered client picks its own name, so that name is untrusted text
// on every surface it reaches — including the toast title, which is the one an
// operator reads while deciding whether to approve an action.
//
// The approval queue already cleaned it. The notification did not, which is the
// asymmetry this covers: the same string arriving redacted in one place and raw
// in the other is a boundary with a hole in whichever half someone forgets.
func TestNotificationCleansTheClientName(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	recorder := &recordingNotifier{}
	e.svc.notifier = recorder

	p := principalFor(e.grant)
	// Zero-width joiners and a bidi override: invisible to the operator reading
	// the toast, perfectly legible to everything else.
	p.ClientName = "Claude\u200b Code\u202e ton.eldnaH"

	if _, _, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "unresponsive",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("raised %d events, want 1", len(recorder.events))
	}

	evt := recorder.events[0]
	name, _ := evt.Data["client_name"].(string)
	for _, invisible := range []string{"\u200b", "\u202e"} {
		if strings.Contains(evt.Title, invisible) {
			t.Errorf("notification title carries %q: %q", invisible, evt.Title)
		}
		if strings.Contains(name, invisible) {
			t.Errorf("notification payload carries %q: %q", invisible, name)
		}
	}
	if !strings.Contains(evt.Title, "Claude") {
		t.Errorf("cleaning destroyed the readable name: %q", evt.Title)
	}
}

// The resolution alert says who acted, so it gets the same treatment — it is
// the alert an operator sees when auto-approval ran something unattended.
func TestResolutionAlertCleansTheClientName(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	recorder := &recordingNotifier{}
	e.svc.notifier = recorder

	req, err := e.store.CreateMCPActionRequest(ctx, e.grant.ID, e.ownerID, e.serverA, "restart", "because")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.notifyActionResolved(ctx, req, "Evil\u202eClient", e.ownerID)

	if len(recorder.events) != 1 {
		t.Fatalf("raised %d events, want 1", len(recorder.events))
	}
	if name, _ := recorder.events[0].Data["client_name"].(string); strings.Contains(name, "\u202e") {
		t.Errorf("resolution alert carries a bidi override: %q", name)
	}
	if recorder.events[0].Type != notify.EventMCPActionResolved {
		t.Fatalf("unexpected event type %q", recorder.events[0].Type)
	}
}

// The audit trail is read by a human too, and auditOperator already cleaned the
// name. The action-request entries did not.
func TestActionAuditCleansTheClientName(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	p := principalFor(e.grant)
	p.ClientName = "Claude\u202eCode"
	if _, _, err := e.svc.requestServerAction(p)(ctx, nil, requestActionInput{
		ServerID: e.serverA, Action: "restart", Reason: "unresponsive",
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := e.store.ListAudit(ctx, e.serverA, 25)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action != "mcp.action.requested" {
			continue
		}
		found = true
		if entry.Detail == nil {
			t.Fatal("audit entry has no detail")
		}
		// Both spellings: the detail is stored as JSON, so the character may
		// survive raw or as its escape sequence.
		if strings.Contains(*entry.Detail, "\u202e") || strings.Contains(*entry.Detail, `\u202e`) {
			t.Errorf("audit detail carries a bidi override: %s", *entry.Detail)
		}
	}
	if !found {
		t.Fatal("no mcp.action.requested audit entry was written")
	}
}

// The reason is bounded at a rune budget, not a byte one. Slicing bytes cut a
// multi-byte character in half and stored invalid UTF-8.
func TestActionReasonIsTruncatedOnRuneBoundaries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// 600 three-byte runes: well past the 500-rune cap, and every byte boundary
	// inside the cut is mid-character.
	req, err := e.store.CreateMCPActionRequest(ctx, e.grant.ID, e.ownerID, e.serverA, "restart",
		strings.Repeat("あ", 600))
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(req.Reason) {
		t.Errorf("stored reason is not valid UTF-8: %q", req.Reason)
	}
	if got := utf8.RuneCountInString(req.Reason); got != 500 {
		t.Errorf("stored reason is %d runes, want 500", got)
	}
}

// ── Bounded waits ────────────────────────────────────────────────

// await_action_request is the only tool call that deliberately does nothing for
// two and a half minutes, and the transport is stateless JSON, so each open
// wait is a held connection, a goroutine, and a database read every two
// seconds. The rate limiter counts calls arriving, not calls still running, so
// one delegation could otherwise stack them without bound.
//
// Tripping the cap is not an error: the tool already documents "still pending,
// call again" as a normal outcome, so a caller at its limit behaves exactly as
// it does on a timeout.
func TestConcurrentWaitsAreBoundedPerGrant(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	req, err := e.store.CreateMCPActionRequest(ctx, e.grant.ID, e.ownerID, e.serverA, "restart", "unresponsive")
	if err != nil {
		t.Fatal(err)
	}

	// Occupy every slot, as in-flight waits would.
	for i := 0; i < maxConcurrentWaits; i++ {
		if !e.svc.beginWait(e.grant.ID) {
			t.Fatalf("slot %d was refused below the cap", i)
		}
	}
	defer func() {
		for i := 0; i < maxConcurrentWaits; i++ {
			e.svc.endWait(e.grant.ID)
		}
	}()

	// The next wait must return at once rather than joining them. If the cap
	// were not enforced this call would block for awaitBudget and the test
	// would time out rather than fail.
	_, out, err := e.svc.awaitActionRequest(p)(ctx, nil, awaitActionInput{RequestID: req.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionPending {
		t.Fatalf("status = %q, want pending", out.Status)
	}
	if out.Note != awaitBusyNote {
		t.Fatalf("note = %q, want the busy note", out.Note)
	}
}

// A finished wait gives its slot back, and the last one out removes the key so
// an idle panel does not keep a row per grant it has ever seen.
func TestWaitSlotsAreReleased(t *testing.T) {
	e := newEnv(t)

	for i := 0; i < maxConcurrentWaits; i++ {
		if !e.svc.beginWait("g1") {
			t.Fatalf("slot %d was refused below the cap", i)
		}
	}
	if e.svc.beginWait("g1") {
		t.Fatal("a slot was handed out past the cap")
	}
	// A different delegation has its own budget.
	if !e.svc.beginWait("g2") {
		t.Fatal("one grant's waits consumed another's budget")
	}
	e.svc.endWait("g2")

	for i := 0; i < maxConcurrentWaits; i++ {
		e.svc.endWait("g1")
	}
	if !e.svc.beginWait("g1") {
		t.Fatal("slots were not released")
	}
	e.svc.endWait("g1")

	e.svc.waitsMu.Lock()
	defer e.svc.waitsMu.Unlock()
	if len(e.svc.waits) != 0 {
		t.Fatalf("wait table retained %d keys after every wait finished", len(e.svc.waits))
	}
}

// A settled request must not consume a slot at all: the wait returns before it
// would ever claim one.
func TestSettledRequestNeedsNoWaitSlot(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	req, err := e.store.CreateMCPActionRequest(ctx, e.grant.ID, e.ownerID, e.serverA, "restart", "unresponsive")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.DenyMCPActionRequest(ctx, req.ID, e.ownerID, e.ownerID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxConcurrentWaits; i++ {
		e.svc.beginWait(e.grant.ID)
	}

	_, out, err := e.svc.awaitActionRequest(p)(ctx, nil, awaitActionInput{RequestID: req.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionDenied {
		t.Fatalf("status = %q, want denied", out.Status)
	}
}
