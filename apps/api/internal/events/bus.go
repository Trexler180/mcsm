// Package events is the panel's live event bus: one ordered stream of typed
// events (status changes, operation progress, stats samples, alerts) that the
// dashboard subscribes to instead of polling each endpoint.
//
// Every event gets a sequence number. A client that reconnects passes the last
// number it saw and receives what it missed from a bounded in-memory ring; if
// the ring has moved past that point, it is told so and refetches instead of
// silently showing a stale screen. That is what lets the dashboard say "caught
// up" honestly after a dropped connection.
//
// The bus is in-process, like the ticket store and the notification hub; a
// multi-node API would back it with a shared broker.
package events

import (
	"sync"
	"time"
)

// Event types published on the bus.
const (
	ServerStatus     = "server.status"
	ServerStats      = "server.stats"
	ServerPlayers    = "server.players"
	ServerUpdated    = "server.updated"
	ServerDeleted    = "server.deleted"
	NodeStatus       = "node.status"
	OperationUpdated = "operation.updated"
	TaskRun          = "task.run"
	Notification     = "notification"
	Activity         = "activity"
	// StreamReset tells a client its resume point is gone: refetch, then
	// continue from the sequence number it carries.
	StreamReset = "stream.reset"
)

// Event is one thing that happened.
type Event struct {
	Seq      int64     `json:"seq"`
	Type     string    `json:"type"`
	At       time.Time `json:"at"`
	ServerID string    `json:"server_id,omitempty"`
	NodeID   string    `json:"node_id,omitempty"`
	// UserID, when set, addresses the event to one user only (their own
	// notification, say). It is never sent to anyone else.
	UserID string `json:"-"`
	// AdminOnly events reach global administrators only.
	AdminOnly bool `json:"-"`
	Data      any  `json:"data,omitempty"`
}

// ringSize bounds how far back a reconnecting client can resume.
const ringSize = 4096

// subBuffer is how many events a subscriber may fall behind before it is cut
// off (and told to refetch on reconnect) rather than slowing the publisher.
const subBuffer = 512

// Bus fans events out to subscribers. The zero value is not usable; use New.
type Bus struct {
	mu   sync.Mutex
	seq  int64
	ring []Event
	subs map[*Subscription]struct{}
	now  func() time.Time
}

func New() *Bus {
	return &Bus{subs: map[*Subscription]struct{}{}, now: time.Now}
}

// Publish stamps e with the next sequence number and delivers it. It never
// blocks: a subscriber whose buffer is full is closed as lagging.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	if e.At.IsZero() {
		e.At = b.now()
	}
	if len(b.ring) >= ringSize {
		copy(b.ring, b.ring[1:])
		b.ring = b.ring[:len(b.ring)-1]
	}
	b.ring = append(b.ring, e)
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			s.lagged = true
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

// Seq is the sequence number of the most recent event.
func (b *Bus) Seq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Subscription is one live consumer.
type Subscription struct {
	ch     chan Event
	lagged bool
	bus    *Bus
}

// C delivers events. It is closed when the subscription ends, including when
// the subscriber fell too far behind (Lagged then reports true).
func (s *Subscription) C() <-chan Event { return s.ch }

// Lagged reports whether the subscription was closed for falling behind.
func (s *Subscription) Lagged() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.lagged
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if _, ok := s.bus.subs[s]; ok {
		delete(s.bus.subs, s)
		close(s.ch)
	}
}

// Subscribe starts a subscription. since is the last sequence number the
// client saw (0 for a fresh client). backlog holds the events after since that
// are still in the ring; resumed is false when since is older than the ring,
// meaning the client missed events it can no longer get and must refetch.
func (b *Bus) Subscribe(since int64) (sub *Subscription, backlog []Event, resumed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub = &Subscription{ch: make(chan Event, subBuffer), bus: b}
	b.subs[sub] = struct{}{}

	resumed = true
	if since > 0 {
		oldest := b.seq + 1
		if len(b.ring) > 0 {
			oldest = b.ring[0].Seq
		}
		if since < oldest-1 {
			resumed = false
		}
		for _, e := range b.ring {
			if e.Seq > since {
				backlog = append(backlog, e)
			}
		}
	}
	return sub, backlog, resumed
}
