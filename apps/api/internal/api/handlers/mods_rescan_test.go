package handlers

import (
	"testing"
	"time"
)

// Loads of the Mods tab share one background rescan per server, and a rescan
// is not repeated while the last one is fresh.
func TestModRescanIsSingleFlightAndCached(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Modded")
	h := NewModHandlers(f.store, t.TempDir(), nil, nil)

	first, state := h.startRescan(srv.ID, false)
	if state != rescanStarted {
		t.Fatalf("first load: %s, want started", state)
	}
	if _, state := h.startRescan(srv.ID, false); state != rescanRunning && state != rescanFresh {
		t.Fatalf("second load during a rescan: %s", state)
	}
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the rescan never finished")
	}
	if _, state := h.startRescan(srv.ID, false); state != rescanFresh {
		t.Fatalf("load right after a rescan: %s, want fresh", state)
	}
	// An explicit rescan ignores freshness.
	forced, state := h.startRescan(srv.ID, true)
	if state != rescanStarted {
		t.Fatalf("forced rescan: %s, want started", state)
	}
	<-forced
}
