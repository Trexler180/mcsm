package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

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

// audit records an action attributed to the current user (from JWT claims) and
// the caller IP. Fire-and-forget; never blocks the response on logging.
func audit(s *store.Store, r *http.Request, serverID, action string, detail any) {
	userID := ""
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		userID = c.UserID
	}
	s.LogAction(r.Context(), userID, serverID, action, clientIP(r), detail)
}
