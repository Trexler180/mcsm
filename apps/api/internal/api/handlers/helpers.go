package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	apimw "github.com/mcsm/api/internal/api/middleware"
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

// clientIP is the caller's address as established by the router's
// TrustedProxy middleware. It never reads forwarding headers itself: the
// leftmost X-Forwarded-For entry is whatever the client chose to send.
func clientIP(r *http.Request) string {
	return apimw.ClientIP(r)
}

// currentUserID returns the authenticated user's ID from JWT claims, or "".
func currentUserID(r *http.Request) string {
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		return c.UserID
	}
	if d := auth.DelegatedActorFrom(r.Context()); d != nil {
		return d.UserID
	}
	return ""
}

// audit records an action attributed to the current user and, when the request
// arrived on an agent access key, to that key as well — so a machine action
// names both the human who owns the credential and which credential it was.
// Fire-and-forget; never blocks the response on logging.
func audit(s *store.Store, r *http.Request, serverID, action string, detail any) {
	if d := auth.DelegatedActorFrom(r.Context()); d != nil {
		_ = s.LogActionWithActor(r.Context(), d.UserID, "", d.GrantID, serverID, action, d.IP, map[string]any{
			"client_name": d.ClientName,
			"operation":   detail,
		})
		return
	}
	userID := currentUserID(r)
	keyID := ""
	if m := auth.MachineFrom(r.Context()); m != nil {
		keyID = m.KeyID
	}
	_ = s.LogActionWithKey(r.Context(), userID, keyID, serverID, action, clientIP(r), detail)
}
