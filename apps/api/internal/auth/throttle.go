package auth

import (
	"sync"
	"time"
)

// LoginThrottle is an in-memory failed-attempt limiter for the login endpoint.
// It is keyed independently by client IP and by account email so neither a
// single IP spraying many accounts nor a botnet hammering one account can run
// unbounded guesses: a key that crosses the free-attempt threshold is locked
// for an exponentially growing window. Successful logins reset the key.
//
// State is per-process (matching the single-API supported shape); a multi-node
// deployment would move this to a shared store. Entries are swept lazily.
type LoginThrottle struct {
	mu      sync.Mutex
	entries map[string]*attemptEntry

	freeAttempts int           // failures allowed before lockouts begin
	baseDelay    time.Duration // first lockout duration
	maxDelay     time.Duration // cap on the backoff window
}

type attemptEntry struct {
	failures   int
	lockedfor  time.Duration
	lockedTill time.Time
	seen       time.Time
}

// NewLoginThrottle returns the default (IP-keyed) throttle: a few free attempts,
// then an exponential lockout up to 15 minutes. This is the aggressive profile —
// appropriate for an IP, where locking out a single attacker is the goal.
func NewLoginThrottle() *LoginThrottle {
	return NewLoginThrottleWith(5, 30*time.Second, 15*time.Minute)
}

// NewAccountThrottle returns the lenient profile used to key on the targeted
// account (email). It allows more free attempts and caps the lockout window
// short, so an attacker can't deny a legitimate user access to their account for
// long by deliberately failing logins against their email — while still slowing
// a distributed guess against one account. The IP throttle remains the hard stop.
func NewAccountThrottle() *LoginThrottle {
	return NewLoginThrottleWith(10, 15*time.Second, 90*time.Second)
}

// NewLoginThrottleWith builds a throttle with explicit parameters.
func NewLoginThrottleWith(freeAttempts int, baseDelay, maxDelay time.Duration) *LoginThrottle {
	return &LoginThrottle{
		entries:      make(map[string]*attemptEntry),
		freeAttempts: freeAttempts,
		baseDelay:    baseDelay,
		maxDelay:     maxDelay,
	}
}

// Allowed reports whether a login attempt for key may proceed right now. When it
// is locked, retryAfter is the remaining wait.
func (t *LoginThrottle) Allowed(key string) (ok bool, retryAfter time.Duration) {
	if key == "" {
		return true, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked()
	e := t.entries[key]
	if e == nil {
		return true, 0
	}
	if now := time.Now(); now.Before(e.lockedTill) {
		return false, e.lockedTill.Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt for key and (re)arms the lockout once the free
// attempts are spent. Call this on every authentication failure.
func (t *LoginThrottle) Fail(key string) {
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[key]
	if e == nil {
		e = &attemptEntry{}
		t.entries[key] = e
	}
	e.failures++
	e.seen = time.Now()
	if e.failures <= t.freeAttempts {
		return
	}
	if e.lockedfor == 0 {
		e.lockedfor = t.baseDelay
	} else {
		e.lockedfor *= 2
		if e.lockedfor > t.maxDelay {
			e.lockedfor = t.maxDelay
		}
	}
	e.lockedTill = e.seen.Add(e.lockedfor)
}

// Reset clears all state for key after a successful login.
func (t *LoginThrottle) Reset(key string) {
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// sweepLocked drops entries that are unlocked and idle, so the map can't grow
// without bound. Caller holds the lock.
func (t *LoginThrottle) sweepLocked() {
	now := time.Now()
	for k, e := range t.entries {
		if now.After(e.lockedTill) && now.Sub(e.seen) > t.maxDelay {
			delete(t.entries, k)
		}
	}
}
