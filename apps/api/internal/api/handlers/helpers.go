package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/mcsm/api/internal/api/apierr"
	apimw "github.com/mcsm/api/internal/api/middleware"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError answers in the API's error envelope with the status's default
// code (see apierr). Prefer writeCodedError when the client can act on a more
// specific code.
func writeError(w http.ResponseWriter, status int, msg string) {
	apierr.Write(w, status, "", msg)
}

// writeServerError logs the real error server-side (with request context) and
// returns a generic 500 to the client, so internal details — SQL text, file
// paths, agent URLs — never leak to a caller probing the API.
func writeServerError(w http.ResponseWriter, r *http.Request, context string, err error) {
	slog.Error("request failed", "context", context, "err", err, "path", r.URL.Path, "method", r.Method)
	apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "internal error")
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

// writeLookupError answers a failed store lookup: 404 when the row does not
// exist, and a logged 500 for anything else, so a database failure no longer
// reads as a missing server.
func writeLookupError(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+" not found")
		return
	}
	writeServerError(w, r, "lookup "+what, err)
}

// auditOutcome records a proxied mutation with the status the node answered,
// so a refused change reads as a refusal in the log rather than as done.
func auditOutcome(s *store.Store, r *http.Request, serverID, action string, detail map[string]any, status int) {
	if detail == nil {
		detail = map[string]any{}
	}
	if status == 0 {
		status = http.StatusOK
	}
	detail["status"] = status
	if status >= 400 {
		detail["refused"] = true
	}
	audit(s, r, serverID, action, detail)
}

// peekJSON reads a small JSON body into dst and restores it for the proxy.
// Bodies over the limit are left unread and reported as not decoded.
func peekJSON(r *http.Request, dst any) bool {
	if r.Body == nil || r.ContentLength > maxPeekBody {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPeekBody+1))
	if err != nil || len(body) > maxPeekBody {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return json.Unmarshal(body, dst) == nil
}

const maxPeekBody = 64 << 10

// emailFingerprint is a short, stable stand-in for an email address in the
// audit log: enough to see repeated attempts against one account, not enough
// to read the address back.
func emailFingerprint(email string) string {
	sum := sha256.Sum256([]byte(store.NormalizeEmail(email)))
	return hex.EncodeToString(sum[:6])
}

// callerIsAdmin reports whether the caller holds the global admin role right
// now, read from the database. The role baked into an access token is up to 15
// minutes old, so trusting it kept a demoted admin's fleet-wide view alive
// until the token expired. An error means the caller's account is gone or
// unreadable; treat it as unauthorized.
func callerIsAdmin(r *http.Request, s *store.Store) (bool, error) {
	uid := currentUserID(r)
	if uid == "" {
		return false, errors.New("no caller")
	}
	u, err := s.GetUserByID(r.Context(), uid)
	if err != nil {
		return false, err
	}
	return u.Role == "admin", nil
}

// writeCodedError writes an error with a stable machine-readable code next to
// the human message, so a client can decide what to do without parsing prose.
func writeCodedError(w http.ResponseWriter, status int, code, msg string) {
	apierr.Write(w, status, code, msg)
}

// currentSessionID is the session the caller's access token belongs to, or ""
// for a machine principal or a session-less token.
func currentSessionID(r *http.Request) string {
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		return c.SessionID
	}
	return ""
}

// writeFieldError rejects a request because of one input field, naming it so a
// form can mark the field rather than show a banner.
func writeFieldError(w http.ResponseWriter, field, msg string) {
	apierr.WriteBody(w, http.StatusBadRequest, apierr.Body{Error: msg, Code: apierr.CodeValidation, Field: field})
}
