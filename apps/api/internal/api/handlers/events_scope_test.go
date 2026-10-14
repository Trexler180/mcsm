package handlers

import (
	"context"
	"testing"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/events"
)

// The live stream shows each user only what they may see: their servers and
// those servers' nodes, their own alerts, and admin events only to admins.
func TestEventScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	alice := f.user("alice@example.com", "user")
	bob := f.user("bob@example.com", "user")
	mine := f.server(alice, "Alice SMP")
	theirs := f.server(bob, "Bob SMP")
	h := NewEventHandlers(f.store, events.New())

	scopeFor := func(id string) *scope {
		sc := &scope{}
		if !h.load(ctx, sc, &auth.Claims{UserID: id}) {
			t.Fatalf("could not load the scope for %s", id)
		}
		return sc
	}
	aliceScope, adminScope := scopeFor(alice.ID), scopeFor(admin.ID)

	cases := []struct {
		name         string
		ev           events.Event
		alice, admin bool
	}{
		{"own server", events.Event{Type: events.ServerStatus, ServerID: mine.ID}, true, true},
		{"someone else's server", events.Event{Type: events.ServerStatus, ServerID: theirs.ID}, false, true},
		{"the node hosting her server", events.Event{Type: events.NodeStatus, NodeID: f.node.ID}, true, true},
		{"another node", events.Event{Type: events.NodeStatus, NodeID: "other-node"}, false, true},
		{"her own alert", events.Event{Type: events.Notification, UserID: alice.ID}, true, false},
		{"bob's alert", events.Event{Type: events.Notification, UserID: bob.ID}, false, false},
		{"admin-only activity", events.Event{Type: events.Activity, AdminOnly: true}, false, true},
		{"an event about nothing in particular", events.Event{Type: events.Activity}, false, true},
	}
	for _, c := range cases {
		if got := aliceScope.allows(c.ev, alice.ID); got != c.alice {
			t.Errorf("%s: alice sees it = %v", c.name, got)
		}
		if got := adminScope.allows(c.ev, admin.ID); got != c.admin {
			t.Errorf("%s: admin sees it = %v", c.name, got)
		}
	}

	// A revoked session stops the stream on its next refresh.
	if h.load(ctx, &scope{}, &auth.Claims{UserID: alice.ID, SessionID: "no-such-session"}) {
		t.Fatal("a stream kept loading for a session that does not exist")
	}
}
