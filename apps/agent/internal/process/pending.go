package process

import (
	"context"
	"errors"
	"time"
)

// A start spends its first minutes, sometimes many, fetching or building the
// server's runtime before there is a process to stop. Without a record of that
// phase a kill or a purge found nothing to act on: the kill failed, the purge
// raced the download for the directory, and the start then launched a server
// nobody wanted any more.

// ErrStartCancelled means a stop, kill or purge cancelled a start before it
// launched the server.
var ErrStartCancelled = errors.New("start cancelled")

// errStartPending refuses a second start while one is still preparing.
var errStartPending = errors.New("server already running")

type pendingStart struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// PendingStart is a start that is preparing its runtime. Its context is
// cancelled when the server is stopped, killed or purged meanwhile.
type PendingStart struct {
	m  *Manager
	id string
	p  *pendingStart
}

// BeginStart records a start that is about to prepare the server's runtime.
// Every BeginStart must be followed by Launch or Abandon.
func (m *Manager) BeginStart(id string, timeout time.Duration) (*PendingStart, error) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if _, ok := m.pending[id]; ok {
		return nil, errStartPending
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	p := &pendingStart{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	m.pending[id] = p
	return &PendingStart{m: m, id: id, p: p}, nil
}

// Context is cancelled when the start is cancelled or runs out of time.
func (ps *PendingStart) Context() context.Context { return ps.p.ctx }

// Launch starts the server unless the start was cancelled meanwhile. The check
// and the launch happen under one lock, so a cancel lands either before (and
// nothing launches) or after (and finds the process to kill).
func (ps *PendingStart) Launch(cfg StartConfig) error {
	ps.m.pendingMu.Lock()
	err := ps.p.ctx.Err()
	if err == nil {
		err = ps.m.Start(ps.id, cfg)
	} else if errors.Is(err, context.Canceled) {
		err = ErrStartCancelled
	}
	ps.m.pendingMu.Unlock()
	ps.finish()
	return err
}

// Abandon ends a start that failed before it could launch.
func (ps *PendingStart) Abandon() { ps.finish() }

func (ps *PendingStart) finish() {
	ps.m.pendingMu.Lock()
	if ps.m.pending[ps.id] == ps.p {
		delete(ps.m.pending, ps.id)
	}
	ps.m.pendingMu.Unlock()
	ps.p.cancel()
	select {
	case <-ps.p.done:
	default:
		close(ps.p.done)
	}
}

// cancelPending cancels a start that is still preparing and waits for it to
// give up, so the files it was writing are released. It reports whether there
// was one.
func (m *Manager) cancelPending(id string) bool {
	m.pendingMu.Lock()
	p, ok := m.pending[id]
	m.pendingMu.Unlock()
	if !ok {
		return false
	}
	p.cancel()
	<-p.done
	return true
}

// Starting reports whether a start is preparing the server's runtime.
func (m *Manager) Starting(id string) bool {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	_, ok := m.pending[id]
	return ok
}
