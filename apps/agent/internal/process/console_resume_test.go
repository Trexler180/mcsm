package process

import (
	"testing"
	"time"
)

func drain(ch <-chan ConsoleEvent, n int, t *testing.T) []ConsoleEvent {
	t.Helper()
	var out []ConsoleEvent
	deadline := time.After(2 * time.Second)
	for len(out) < n {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			t.Fatalf("got %d of %d console lines", len(out), n)
		}
	}
	return out
}

// A client that reconnects to the same run gets only the lines it missed, in
// order; one that asks about another run, or about lines no longer in memory,
// starts over with the whole history.
func TestConsoleResumesWhereTheClientLeftOff(t *testing.T) {
	m := NewManager(t.TempDir())
	newConsoleServer(t, m, "srv")
	inst := m.instances["srv"]
	inst.runID = "run-1"
	for _, l := range []string{"one", "two", "three"} {
		inst.broadcastCh <- ConsoleEvent{Line: l}
	}
	first, err := m.SubscribeConsole("srv", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := drain(first.Events, 3, t)
	first.Cancel()
	if first.Resumed || got[0].Seq != 1 || got[2].Seq != 3 || got[2].Line != "three" {
		t.Fatalf("first connection: resumed=%v %+v", first.Resumed, got)
	}

	inst.broadcastCh <- ConsoleEvent{Line: "four"}
	again, err := m.SubscribeConsole("srv", "run-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	got = drain(again.Events, 1, t)
	if !again.Resumed || got[0].Line != "four" || got[0].Seq != 4 {
		t.Fatalf("resume: resumed=%v %+v", again.Resumed, got)
	}
	// Live lines follow the replayed ones.
	inst.broadcastCh <- ConsoleEvent{Line: "five"}
	if got := drain(again.Events, 1, t); got[0].Line != "five" {
		t.Fatalf("live line %+v", got)
	}
	again.Cancel()

	other, _ := m.SubscribeConsole("srv", "some-old-run", 3)
	if other.Resumed || len(drain(other.Events, 5, t)) != 5 {
		t.Fatal("a different run did not start over")
	}
	other.Cancel()

	// Lines that fell out of the ring cannot be resumed from.
	inst.histMu.Lock()
	inst.history = inst.history[3:] // keep only seq 4 and 5
	inst.histMu.Unlock()
	gap, _ := m.SubscribeConsole("srv", "run-1", 1)
	if gap.Resumed {
		t.Fatal("resumed across lines no longer in memory")
	}
	gap.Cancel()

	if _, err := m.SubscribeConsole("missing", "", 0); err == nil {
		t.Fatal("subscribed to a server that is not running")
	}
}
