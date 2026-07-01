package notify

import (
	"encoding/json"
	"sync"
)

// Hub is the in-process registry of connected notification streams, keyed by
// user. Push fans a payload out to every live connection a user has open. Like
// the auth ticket store it is single-process scope; a multi-node API would need
// a shared pub/sub backend.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[chan []byte]struct{})}
}

// Subscribe registers a new stream for userID and returns its receive channel
// plus an unsubscribe func the caller must defer. The channel is buffered so a
// momentarily slow consumer doesn't block Push; an overflowing client simply
// drops the surplus (the feed API backfills missed items on reconnect).
func (h *Hub) Subscribe(userID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	set := h.clients[userID]
	if set == nil {
		set = make(map[chan []byte]struct{})
		h.clients[userID] = set
	}
	set[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if set := h.clients[userID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.clients, userID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
}

// Push delivers payload to all of a user's connections. Best-effort and
// non-blocking: a full client buffer drops the message rather than stalling the
// emitter.
func (h *Hub) Push(userID string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients[userID] {
		select {
		case ch <- data:
		default:
		}
	}
}

// Connected reports whether the user currently has at least one live stream —
// used only for diagnostics/tests.
func (h *Hub) Connected(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID]) > 0
}
