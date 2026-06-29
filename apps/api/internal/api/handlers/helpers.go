package handlers

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeServerError logs the real error server-side (with request context) and
// returns a generic 500 to the client, so internal details — SQL text, file
// paths, agent URLs — never leak to a caller probing the API.
func writeServerError(w http.ResponseWriter, r *http.Request, context string, err error) {
	slog.Error("request failed", "context", context, "err", err, "path", r.URL.Path, "method", r.Method)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// tooManyRequests writes a 429 with a Retry-After hint.
func tooManyRequests(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "too many login attempts; try again later")
}

// userAgent returns a length-bounded User-Agent for session display, so a
// hostile header can't bloat a stored row.
func userAgent(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if len(ua) > 400 {
		ua = ua[:400]
	}
	return ua
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// clientIP extracts the best-effort caller IP, honoring X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// currentUserID returns the authenticated user's ID from JWT claims, or "".
func currentUserID(r *http.Request) string {
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		return c.UserID
	}
	return ""
}

// audit records an action attributed to the current user (from JWT claims) and
// the caller IP. Fire-and-forget; never blocks the response on logging.
func audit(s *store.Store, r *http.Request, serverID, action string, detail any) {
	userID := ""
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		userID = c.UserID
	}
	s.LogAction(r.Context(), userID, serverID, action, clientIP(r), detail)
}
