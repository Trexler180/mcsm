package poller

import (
	"encoding/json"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// statusWith builds a status payload the way GetStatus decodes one: generic
// JSON, not the agent's typed struct.
func statusWith(denials ...map[string]any) map[string]any {
	blob, _ := json.Marshal(map[string]any{"status": "online", "join_denied": denials})
	var out map[string]any
	_ = json.Unmarshal(blob, &out)
	return out
}

func denial(name string, firstAt int64, attempts int) map[string]any {
	return map[string]any{"name": name, "first_at": firstAt, "attempts": attempts}
}

// The agent keeps reporting a denial until it is resolved, which is what makes
// the panel's list mean "still waiting". The poller must not re-alert on every
// one of those repeats.
func TestDenialTrackerAlertsOncePerRecord(t *testing.T) {
	tr := newDenialTracker()
	srv := &store.Server{ID: "srv1", Name: "Survival"}

	// engine is nil, so Emit is a no-op; what is under test is which records the
	// tracker considers new, which its ledger records exactly.
	tr.observe(srv, statusWith(denial("Steve", 1000, 1)), nil)
	if got := len(tr.alerted["srv1"]); got != 1 {
		t.Fatalf("ledger holds %d records, want 1", got)
	}

	// Same record, now with a retry counted. Already alerted on.
	tr.observe(srv, statusWith(denial("Steve", 1000, 4)), nil)
	if got := len(tr.alerted["srv1"]); got != 1 {
		t.Fatalf("a repeat report must not add a record, ledger = %v", tr.alerted["srv1"])
	}

	// The same player returning later is a distinct record: a new FirstAt.
	tr.observe(srv, statusWith(denial("Steve", 1000, 4), denial("Steve", 9000, 1)), nil)
	if got := len(tr.alerted["srv1"]); got != 2 {
		t.Fatalf("a later visit should be its own record, ledger = %v", tr.alerted["srv1"])
	}
}

// Resolving a denial in the middle of the list must not disturb the others — the
// bug that a high-water sequence cursor would have had.
func TestDenialTrackerHandlesOutOfOrderResolution(t *testing.T) {
	tr := newDenialTracker()
	srv := &store.Server{ID: "srv1", Name: "Survival"}

	tr.observe(srv, statusWith(denial("Steve", 1000, 1), denial("Alex", 2000, 1)), nil)
	// Alex (the newer record) is whitelisted, so the agent stops reporting them.
	tr.observe(srv, statusWith(denial("Steve", 1000, 2)), nil)

	if _, ok := tr.alerted["srv1"][joinDenial{Name: "Steve", FirstAt: 1000}.key()]; !ok {
		t.Fatal("Steve's record should still be marked as alerted")
	}
	if len(tr.alerted["srv1"]) != 1 {
		t.Fatalf("Alex's resolved record should have been dropped, ledger = %v", tr.alerted["srv1"])
	}

	// And Steve must not be re-alerted now that he is the newest record.
	tr.observe(srv, statusWith(denial("Steve", 1000, 3)), nil)
	if len(tr.alerted["srv1"]) != 1 {
		t.Fatalf("ledger = %v, want Steve alone and unchanged", tr.alerted["srv1"])
	}
}

func TestDenialTrackerForgetsEmptyAndDeletedServers(t *testing.T) {
	tr := newDenialTracker()
	srv := &store.Server{ID: "srv1", Name: "Survival"}

	tr.observe(srv, statusWith(denial("Steve", 1000, 1)), nil)
	tr.observe(srv, statusWith(), nil)
	if _, ok := tr.alerted["srv1"]; ok {
		t.Error("an empty report means nobody is waiting; the ledger should be gone")
	}

	tr.observe(srv, statusWith(denial("Steve", 1000, 1)), nil)
	tr.reap(map[string]struct{}{"srv2": {}})
	if _, ok := tr.alerted["srv1"]; ok {
		t.Error("a deleted server's ledger should be reaped")
	}
}

// A status payload without the field (an older agent, or a server with nothing
// to report) must be inert rather than a panic or a spurious alert.
func TestDenialTrackerToleratesMissingField(t *testing.T) {
	tr := newDenialTracker()
	srv := &store.Server{ID: "srv1"}
	tr.observe(srv, map[string]any{"status": "online"}, nil)
	tr.observe(srv, map[string]any{"status": "online", "join_denied": nil}, nil)
	tr.observe(srv, map[string]any{"status": "online", "join_denied": "nonsense"}, nil)
	if len(tr.alerted) != 0 {
		t.Fatalf("ledger = %v, want empty", tr.alerted)
	}
}
