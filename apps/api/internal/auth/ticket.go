package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TicketStore hands out short-lived, single-use, opaque IDs that stand in for a
// caller's identity on requests that cannot carry an Authorization header:
// browser file downloads (plain navigations) and WebSocket handshakes (console,
// metrics). The previous scheme put the raw 15-minute JWT in the query string,
// which leaks through history, logs, and referrers. A ticket is random, expires
// in seconds, and is consumed on first use, so a leaked URL is inert almost
// immediately.
//
// Storage is in-memory, so this is scoped to a single API process. A multi-node
// API deployment would need a shared backing store (e.g. Redis).
type TicketStore struct {
	mu      sync.Mutex
	entries map[string]ticketEntry
}

type ticketEntry struct {
	claims  *Claims
	expires time.Time
}

func NewTicketStore() *TicketStore {
	return &TicketStore{entries: make(map[string]ticketEntry)}
}

// Issue stores the claims under a fresh random ID valid for ttl and returns the
// ID. Expired entries are swept opportunistically so no background goroutine is
// needed — ticket volume in this app is low.
func (s *TicketStore) Issue(claims *Claims, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, k)
		}
	}
	s.entries[id] = ticketEntry{claims: claims, expires: now.Add(ttl)}
	return id, nil
}

// Consume returns the claims for id and removes it, so each ticket works once.
// A missing or expired ticket returns ok=false.
func (s *TicketStore) Consume(id string) (*Claims, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	delete(s.entries, id)
	if time.Now().After(e.expires) {
		return nil, false
	}
	return e.claims, true
}
