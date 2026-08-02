package link

import "testing"

// TestMemorySinkDisconnectCallback pins the seam the agent uses to drop derived
// state (the manager's link roster) the moment a mod stops talking, rather than
// letting it age out over a TTL on a server that may already be stopped.
func TestMemorySinkDisconnectCallback(t *testing.T) {
	sink := NewMemorySink()

	var gone []string
	sink.OnDisconnectFunc(func(serverID string) { gone = append(gone, serverID) })

	sink.OnConnect("srv1", Hello{ModVersion: "1.0.1"})
	sink.OnDisconnect("srv1")

	if len(gone) != 1 || gone[0] != "srv1" {
		t.Fatalf("disconnect callback saw %v, want [srv1]", gone)
	}

	st, ok := sink.State("srv1")
	if !ok {
		t.Fatal("state discarded on disconnect; the last snapshot must be retained")
	}
	if st.Connected {
		t.Error("state still reports connected after disconnect")
	}
}

// TestMemorySinkDisconnectWithoutCallback guards the nil-callback path — a
// sink with nothing registered must not panic.
func TestMemorySinkDisconnectWithoutCallback(t *testing.T) {
	sink := NewMemorySink()
	sink.OnConnect("srv1", Hello{})
	sink.OnDisconnect("srv1")
}

// TestMemorySinkForgetEvicts is the counterpart to the retention asserted above.
// Keeping the last snapshot across a disconnect is deliberate, which means
// nothing here ever shrinks on its own; a purged server must therefore be
// evicted explicitly or its entry outlives the server for the agent's lifetime.
func TestMemorySinkForgetEvicts(t *testing.T) {
	sink := NewMemorySink()
	sink.OnConnect("srv1", Hello{ModVersion: "1.0.2"})
	sink.OnDisconnect("srv1")

	if _, ok := sink.State("srv1"); !ok {
		t.Fatal("precondition: disconnect should retain state")
	}

	sink.Forget("srv1")

	if _, ok := sink.State("srv1"); ok {
		t.Error("state survived Forget")
	}
}

// Forgetting a server that was never seen is a no-op, not a panic: purge runs
// for servers that never started, and so never linked.
func TestMemorySinkForgetUnknownServer(t *testing.T) {
	NewMemorySink().Forget("never-seen")
}
