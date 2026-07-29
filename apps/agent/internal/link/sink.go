package link

import (
	"sync"
	"time"
)

// State is the agent's current view of one linked server.
type State struct {
	Connected    bool
	Hello        Hello
	Snapshot     Snapshot
	SnapshotAt   time.Time
	ConnectedAt  time.Time
	LastEvent    Event
	LastEventAt  time.Time
	SnapshotSeen bool
}

// MemorySink keeps the latest state per server in memory.
//
// It deliberately stores only the newest snapshot rather than a history: the
// snapshot is a reconcile anchor, and history belongs in the API's metric
// series, which is durable and already has retention and rollup logic. Keeping
// a second, lossy history here would just be a source of disagreement.
type MemorySink struct {
	mu     sync.RWMutex
	states map[string]*State

	// onSnapshot lets the agent forward vitals into the metrics pipeline without
	// this type having to know anything about it.
	onSnapshot func(serverID string, snap Snapshot)
	onEvent    func(serverID string, ev Event)
}

// NewMemorySink builds a sink. Both callbacks may be nil.
func NewMemorySink() *MemorySink {
	return &MemorySink{states: make(map[string]*State)}
}

// OnSnapshotFunc registers a callback invoked for every snapshot received.
func (s *MemorySink) OnSnapshotFunc(fn func(serverID string, snap Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSnapshot = fn
}

// OnEventFunc registers a callback invoked for every event received.
func (s *MemorySink) OnEventFunc(fn func(serverID string, ev Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEvent = fn
}

// State returns a copy of the current view for a server.
func (s *MemorySink) State(serverID string) (State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[serverID]
	if !ok {
		return State{}, false
	}
	return *st, true
}

// OnConnect implements Sink.
func (s *MemorySink) OnConnect(serverID string, hello Hello) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[serverID] = &State{
		Connected:   true,
		Hello:       hello,
		ConnectedAt: time.Now(),
	}
}

// OnDisconnect implements Sink.
//
// The last snapshot is deliberately retained: it is the most recent truth we
// have, and discarding it would make the panel flip to "unknown" during a
// routine reconnect. Callers distinguish stale from live via Connected.
func (s *MemorySink) OnDisconnect(serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[serverID]; ok {
		st.Connected = false
	}
}

// OnSnapshot implements Sink.
func (s *MemorySink) OnSnapshot(serverID string, snap Snapshot) {
	s.mu.Lock()
	st, ok := s.states[serverID]
	if !ok {
		st = &State{Connected: true, ConnectedAt: time.Now()}
		s.states[serverID] = st
	}
	st.Snapshot = snap
	st.SnapshotAt = time.Now()
	st.SnapshotSeen = true
	fn := s.onSnapshot
	s.mu.Unlock()

	// Invoked outside the lock: a slow consumer must not stall the read loop.
	if fn != nil {
		fn(serverID, snap)
	}
}

// OnEvent implements Sink.
func (s *MemorySink) OnEvent(serverID string, ev Event) {
	s.mu.Lock()
	st, ok := s.states[serverID]
	if !ok {
		st = &State{Connected: true, ConnectedAt: time.Now()}
		s.states[serverID] = st
	}
	st.LastEvent = ev
	st.LastEventAt = time.Now()
	fn := s.onEvent
	s.mu.Unlock()

	if fn != nil {
		fn(serverID, ev)
	}
}
