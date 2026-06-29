package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mcsm/api/internal/auth"
)

// RateLimiter is a per-caller token-bucket limiter. Keyed by authenticated user
// id when available (so one user can't be throttled by another behind the same
// NAT), falling back to client IP. It caps how hard any single caller can hit
// the API — protecting expensive endpoints (mod search, disk reconcile) and the
// box generally — without affecting normal interactive or polling use.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // tokens added per second
	burst   float64 // bucket capacity
	lastGC  time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows roughly perMinute sustained requests with a burst
// allowance for short spikes.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    float64(perMinute) / 60.0,
		burst:   float64(burst),
		lastGC:  time.Now(),
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(rateKey(r)) {
			h := w.Header()
			h.Set("Retry-After", "1")
			h.Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.gcLocked(now)

	b := rl.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}
	// Refill based on elapsed time, capped at burst.
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gcLocked drops idle buckets occasionally so the map can't grow without bound.
func (rl *RateLimiter) gcLocked(now time.Time) {
	if now.Sub(rl.lastGC) < time.Minute {
		return
	}
	rl.lastGC = now
	for k, b := range rl.buckets {
		// A bucket idle long enough to have fully refilled carries no state.
		if now.Sub(b.last) > 10*time.Minute {
			delete(rl.buckets, k)
		}
	}
}

func rateKey(r *http.Request) string {
	if c := auth.ClaimsFrom(r.Context()); c != nil && c.UserID != "" {
		return "u:" + c.UserID
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}
