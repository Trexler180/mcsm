package process

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSavedConfirmationIgnoresChat(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"[12:00:00] [Server thread/INFO]: Saved the game", true},
		{"[12:00:00 INFO]: Saved the game", true},
		{"[12:00:00] [Server thread/INFO]: Saved the world", true},
		{"[12:00:00 INFO]: Save complete.", true},
		{"[12:00:00] [Server thread/INFO]: Saving the game (this may take a moment!)", false},
		// A player typing the phrase must not end the wait early.
		{"[12:00:00] [Server thread/INFO]: <Steve> Saved the game", false},
		{"[12:00:00] [Server thread/INFO]: [Not Secure] <Steve> Saved the game", false},
	} {
		got := !isChatLine(tc.line) && savedRe.MatchString(tc.line)
		if got != tc.want {
			t.Errorf("%q: confirmation = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// consoleServer is a running instance whose stdin records commands and which
// answers "save-all flush" the way a real server does.
type consoleServer struct {
	mu       sync.Mutex
	commands []string
	inst     *Instance
	silent   bool
	replies  sync.WaitGroup
}

func (c *consoleServer) Write(p []byte) (int, error) {
	cmd := strings.TrimSpace(string(p))
	c.mu.Lock()
	c.commands = append(c.commands, cmd)
	silent := c.silent
	c.mu.Unlock()
	if cmd == "save-all flush" && !silent {
		c.replies.Add(1)
		go func() {
			defer c.replies.Done()
			time.Sleep(5 * time.Millisecond)
			// A spoof first: it must be ignored.
			c.inst.broadcastCh <- ConsoleEvent{Line: "[Server thread/INFO]: <Steve> Saved the game", TS: time.Now().UnixMilli()}
			c.inst.broadcastCh <- ConsoleEvent{Line: "[Server thread/INFO]: Saved the game", TS: time.Now().UnixMilli()}
		}()
	}
	return len(p), nil
}

func (c *consoleServer) Close() error { return nil }

func (c *consoleServer) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.commands...)
}

var _ io.WriteCloser = (*consoleServer)(nil)

func newConsoleServer(t *testing.T, m *Manager, id string) *consoleServer {
	t.Helper()
	cs := &consoleServer{}
	inst := &Instance{
		ID:             id,
		status:         StatusOnline,
		done:           make(chan struct{}),
		broadcastCh:    make(chan ConsoleEvent, 16),
		subs:           map[chan ConsoleEvent]struct{}{},
		players:        map[string]time.Time{},
		playersChanged: make(chan struct{}),
	}
	inst.stdin = cs
	cs.inst = inst
	go inst.broadcastLoop()
	t.Cleanup(func() {
		cs.replies.Wait()
		close(inst.broadcastCh)
	})
	m.instances[id] = inst
	return cs
}

func TestQuiesceFlushesAndResumes(t *testing.T) {
	m := NewManager(t.TempDir())
	cs := newConsoleServer(t, m, "srv")

	res, resume := m.Quiesce(context.Background(), "srv", time.Second)
	if !res.Running || !res.Flushed {
		t.Fatalf("result = %+v, want a confirmed flush", res)
	}
	if got := strings.Join(cs.sent(), ","); got != "save-off,save-all flush" {
		t.Fatalf("commands = %s", got)
	}
	resume()
	resume() // idempotent
	if got := strings.Join(cs.sent(), ","); got != "save-off,save-all flush,save-on" {
		t.Fatalf("commands after resume = %s", got)
	}
}

// Overlapping backups: autosave stays off until the last one finishes.
func TestQuiesceIsReferenceCounted(t *testing.T) {
	m := NewManager(t.TempDir())
	cs := newConsoleServer(t, m, "srv")

	_, resumeA := m.Quiesce(context.Background(), "srv", time.Second)
	_, resumeB := m.Quiesce(context.Background(), "srv", time.Second)
	resumeA()
	for _, c := range cs.sent() {
		if c == "save-on" {
			t.Fatal("autosave came back on while another backup was still copying")
		}
	}
	resumeB()
	sent := cs.sent()
	if sent[len(sent)-1] != "save-on" {
		t.Fatalf("autosave was not restored after the last backup: %v", sent)
	}
	var offs int
	for _, c := range sent {
		if c == "save-off" {
			offs++
		}
	}
	if offs != 1 {
		t.Fatalf("save-off sent %d times, want once", offs)
	}
}

func TestQuiesceReportsAnUnconfirmedSave(t *testing.T) {
	m := NewManager(t.TempDir())
	cs := newConsoleServer(t, m, "srv")
	cs.silent = true

	res, resume := m.Quiesce(context.Background(), "srv", 50*time.Millisecond)
	defer resume()
	if !res.Running || res.Flushed {
		t.Fatalf("result = %+v, want running but unconfirmed", res)
	}
}

func TestQuiesceStoppedServerIsANoop(t *testing.T) {
	m := NewManager(t.TempDir())
	res, resume := m.Quiesce(context.Background(), "missing", time.Second)
	resume()
	if res.Running || res.Flushed {
		t.Fatalf("result = %+v for a stopped server", res)
	}
}
