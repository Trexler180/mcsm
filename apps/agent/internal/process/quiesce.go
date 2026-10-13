package process

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"
)

// Copying a running world is only safe while the server is not writing it.
// Quiesce turns autosave off, flushes everything to disk and waits for the
// server to confirm, so the region files a backup reads are complete and
// consistent; the returned resume turns autosave back on.
//
// Backups of the same server may overlap (a scheduled one landing while a
// manual one runs), so saving is reference counted: it is switched off by the
// first and back on by the last, never in the middle of another's copy.

// savedRe matches the server's confirmation that a save-all finished: "Saved
// the game" on current versions, "Saved the world" before 1.13, and Bukkit's
// "Save complete." It is anchored to the log prefix so a player typing the
// phrase in chat cannot fake it.
var savedRe = regexp.MustCompile(`\]:\s+(?:Saved the (?:game|world)|Save complete\.?)\s*$`)

type quiesceState struct {
	mu    sync.Mutex
	count map[string]int
}

// QuiesceResult reports whether a running server confirmed it had flushed.
type QuiesceResult struct {
	// Running is false when the server was not running: its files are already
	// at rest and nothing was sent.
	Running bool
	// Flushed is true once the server confirmed the save. A running server that
	// never confirmed is still backed up, but the copy may be inconsistent.
	Flushed bool
}

var errNotRunning = errors.New("server not running")

// Quiesce prepares a server's files for copying. The returned resume must be
// called once the copy is done; it is safe to call when nothing was paused.
func (m *Manager) Quiesce(ctx context.Context, id string, timeout time.Duration) (QuiesceResult, func()) {
	noop := func() {}
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok || inst.exited() {
		return QuiesceResult{}, noop
	}
	if st := inst.statusInfo().Status; st != StatusOnline {
		// Starting or stopping: the console may not accept commands yet, and a
		// server mid-shutdown saves on its own. Back up as-is.
		return QuiesceResult{Running: true}, noop
	}

	m.quiesce.mu.Lock()
	if m.quiesce.count == nil {
		m.quiesce.count = map[string]int{}
	}
	first := m.quiesce.count[id] == 0
	m.quiesce.count[id]++
	m.quiesce.mu.Unlock()

	var once sync.Once
	resume := func() {
		once.Do(func() {
			m.quiesce.mu.Lock()
			m.quiesce.count[id]--
			last := m.quiesce.count[id] <= 0
			if last {
				delete(m.quiesce.count, id)
			}
			m.quiesce.mu.Unlock()
			if !last {
				return
			}
			if err := m.SendCommand(id, "save-on"); err != nil && !errors.Is(err, errNotRunning) {
				slog.Warn("backup: could not re-enable autosave", "server_id", id, "error", err)
			}
		})
	}

	// Subscribe before sending, so the confirmation cannot be missed. The
	// subscription replays history, so only lines after the request count.
	events, unsub, err := m.Subscribe(id)
	if err != nil {
		resume()
		return QuiesceResult{Running: true}, noop
	}
	defer unsub()
	sent := time.Now().UnixMilli()

	if first {
		if err := m.SendCommand(id, "save-off"); err != nil {
			slog.Warn("backup: save-off failed", "server_id", id, "error", err)
		}
	}
	if err := m.SendCommand(id, "save-all flush"); err != nil {
		slog.Warn("backup: save-all failed", "server_id", id, "error", err)
		return QuiesceResult{Running: true}, resume
	}

	wait := time.NewTimer(timeout)
	defer wait.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// The server exited mid-flush; its shutdown saved the world.
				return QuiesceResult{Running: true}, resume
			}
			if ev.TS >= sent && !isChatLine(ev.Line) && savedRe.MatchString(ev.Line) {
				return QuiesceResult{Running: true, Flushed: true}, resume
			}
		case <-wait.C:
			slog.Warn("backup: server did not confirm the save in time", "server_id", id, "timeout", timeout)
			return QuiesceResult{Running: true}, resume
		case <-ctx.Done():
			return QuiesceResult{Running: true}, resume
		}
	}
}
