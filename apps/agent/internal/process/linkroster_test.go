package process

import (
	"testing"
	"time"
)

// The symptom this guards against: with the helper mod connected, the players
// tab was still typing `/list` into the server console. A mod-reported roster
// must take over that job entirely — otherwise the mod is installed, linked,
// and pointless.
func TestRefreshPlayersPrefersLinkRoster(t *testing.T) {
	m := NewManager(t.TempDir())

	// No instance is registered, so the console path can only return nil. If the
	// link roster is used, we get players back — which is the whole assertion.
	if got := m.RefreshPlayers("srv1", time.Millisecond); got != nil {
		t.Fatalf("precondition: expected no roster without a link, got %v", got)
	}

	m.SetLinkRoster("srv1", []Player{
		{Name: "Steve", UUID: "11111111-2222-3333-4444-555555555555", Online: true},
		{Name: "Alex", UUID: "66666666-7777-8888-9999-000000000000", Online: true},
	})

	got := m.RefreshPlayers("srv1", time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d players from the link roster, want 2", len(got))
	}
	if got[0].Name != "Steve" || got[0].UUID == "" {
		t.Errorf("link roster lost data: %+v", got[0])
	}
	if !m.HasLinkRoster("srv1") {
		t.Error("HasLinkRoster should report the mod as the roster source")
	}
}

// A mod that stops reporting must not freeze the roster forever; the console
// path has to take back over.
func TestLinkRosterExpires(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetLinkRoster("srv1", []Player{{Name: "Steve", Online: true}})

	m.linkMu.Lock()
	entry := m.linkRosters["srv1"]
	entry.at = time.Now().Add(-linkRosterTTL - time.Second)
	m.linkRosters["srv1"] = entry
	m.linkMu.Unlock()

	if m.HasLinkRoster("srv1") {
		t.Error("a stale roster must not be reported as live")
	}
	if got := m.RefreshPlayers("srv1", time.Millisecond); got != nil {
		t.Errorf("stale roster was still served: %v", got)
	}
}

func TestClearLinkRoster(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetLinkRoster("srv1", []Player{{Name: "Steve", Online: true}})

	m.ClearLinkRoster("srv1")

	if m.HasLinkRoster("srv1") {
		t.Error("roster survived being cleared")
	}
}

// Callers stamp op/whitelist/ban flags onto the players they receive, so the
// stored roster must not be aliased into their slice.
func TestLinkRosterReturnsACopy(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetLinkRoster("srv1", []Player{{Name: "Steve", Online: true}})

	first := m.RefreshPlayers("srv1", time.Millisecond)
	first[0].Op = true

	second := m.RefreshPlayers("srv1", time.Millisecond)
	if second[0].Op {
		t.Error("mutating a returned roster changed the stored one")
	}
}

// Per-server isolation: one linked server must not supply another's roster.
func TestLinkRosterIsPerServer(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetLinkRoster("srv1", []Player{{Name: "Steve", Online: true}})

	if m.HasLinkRoster("srv2") {
		t.Error("a roster leaked across servers")
	}
}
