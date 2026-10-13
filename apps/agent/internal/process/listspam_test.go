package process

import (
	"strings"
	"testing"
	"time"
)

// Once a list answer has established the roster, polling the players view
// does not type `list` into the console again until the roster is due for a
// re-check.
func TestRefreshPlayersDoesNotSpamList(t *testing.T) {
	m := NewManager(t.TempDir())
	cs := newConsoleServer(t, m, "srv")
	inst := m.instances["srv"]

	m.RefreshPlayers("srv", time.Millisecond)
	inst.consumeLine("[12:00:00] [Server thread/INFO]: There are 1 of a max of 20 players online: Alex", "stdout")
	for i := 0; i < 5; i++ {
		inst.playersMu.Lock()
		inst.lastListRefresh = time.Time{} // as if the poll came much later
		inst.playersMu.Unlock()
		m.RefreshPlayers("srv", time.Millisecond)
	}
	if n := strings.Count(strings.Join(cs.sent(), ","), "list"); n != 1 {
		t.Fatalf("list sent %d times, want once", n)
	}
	if p := m.RefreshPlayers("srv", time.Millisecond); len(p) != 1 || p[0].Name != "Alex" {
		t.Fatalf("roster = %+v", p)
	}

	// Due for a re-check.
	inst.playersMu.Lock()
	inst.listConfirmed = time.Now().Add(-listReconcileInterval - time.Second)
	inst.lastListRefresh = time.Time{}
	inst.playersMu.Unlock()
	m.RefreshPlayers("srv", time.Millisecond)
	if n := strings.Count(strings.Join(cs.sent(), ","), "list"); n != 2 {
		t.Fatalf("list sent %d times after the roster aged, want 2", n)
	}
}
