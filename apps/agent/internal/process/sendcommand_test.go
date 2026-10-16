package process

import (
	"errors"
	"os"
	"testing"
)

// A server that exited keeps its record, but its console pipe is closed. A
// command sent to it (a schedule firing after a crash, say) must report that
// the server isn't running, not the pipe's write error.
func TestSendCommandToAnExitedServerSaysNotRunning(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	w.Close()
	done := make(chan struct{})
	close(done)
	m := NewManager(t.TempDir())
	m.instances["srv"] = &Instance{ID: "srv", stdin: w, done: done}

	if err := m.SendCommand("srv", "say hello"); !errors.Is(err, errNotRunning) {
		t.Fatalf("SendCommand = %v, want errNotRunning", err)
	}
}

func TestSendCommandOnAClosedPipeSaysNotRunning(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	w.Close()
	m := NewManager(t.TempDir())
	m.instances["srv"] = &Instance{ID: "srv", stdin: w, done: make(chan struct{})}

	if err := m.SendCommand("srv", "say hello"); !errors.Is(err, errNotRunning) {
		t.Fatalf("SendCommand = %v, want errNotRunning", err)
	}
}
