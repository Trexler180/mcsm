package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

type ctxKey int

const (
	claimsKey ctxKey = iota
	machineKey
)

// SessionCheck reports whether the session an access token belongs to is still
// live. It returns ErrSessionRevoked (or any error) to refuse the request.
type SessionCheck func(ctx context.Context, userID, sessionID string) error

// ErrSessionRevoked means the token's session was ended: logout, revocation, a
// password reset, or the account's deletion.
var ErrSessionRevoked = errors.New("session revoked")

// Option configures Middleware.
type Option func(*options)

type options struct {
	sessions SessionCheck
}

// WithSessionCheck makes every access token (and every ticket minted from one)
// prove its session is still live on each request. With it, a token minted
// without a session is refused outright.
func WithSessionCheck(fn SessionCheck) Option {
	return func(o *options) { o.sessions = fn }
}

// Middleware authenticates a request. keys may be nil, which disables machine
// access-key authentication entirely (every mcsm_pat_ bearer then 401s).
func Middleware(secret string, tickets *TicketStore, keys KeyLookup, opts ...Option) func(http.Handler) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	// human admits claims from a JWT or a ticket once their session checks out.
	human := func(w http.ResponseWriter, r *http.Request, next http.Handler, claims *Claims) {
		if o.sessions != nil {
			if claims.SessionID == "" || o.sessions(r.Context(), claims.UserID, claims.SessionID) != nil {
				writeUnauth(w, "session_revoked")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Normal API calls carry the JWT in the Authorization header.
			if token := bearerFromHeader(r); token != "" {
				// Only the reserved prefix reaches key lookup; everything else
				// stays on the JWT path exactly as before.
				if strings.HasPrefix(token, AccessKeyPrefix) {
					ctx, ok := authenticateKey(r.Context(), keys, token, remoteIP(r))
					if !ok {
						// One uniform 401 for every invalid-key state — unknown,
						// expired, revoked, orphaned, malformed, or a store
						// failure — so probing can't distinguish them.
						writeUnauth(w, "unauthorized")
						return
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				claims, err := ParseAccessToken(secret, token)
				if err != nil {
					code := "unauthorized"
					if errors.Is(err, ErrExpiredToken) {
						// The client should refresh and retry, not sign in.
						code = "token_expired"
					}
					writeUnauth(w, code)
					return
				}
				human(w, r, next, claims)
				return
			}

			// Browsers can't set Authorization on a WebSocket handshake, and
			// downloads are plain navigations, so those endpoints accept a
			// short-lived, single-use ?ticket=… instead. We never accept a raw
			// JWT in the query string — that leaks through history and logs.
			// The same rule is stricter for machine keys: they are long-lived,
			// so they are header-only and no ticket is ever minted from one.
			if queryTokenAllowed(r) {
				if claims, ok := tickets.Consume(r.URL.Query().Get("ticket"), r.URL.Path); ok {
					human(w, r, next, claims)
					return
				}
			}
			writeUnauth(w, "unauthorized")
		})
	}
}

// authenticateKey resolves a machine access key and builds the request context:
// ordinary user claims (so every existing handler keeps working unchanged) plus
// a separate machine principal carrying the key's own bounds. The claims are
// populated from the owner's live user row, not from a token payload, so a role
// change or deletion lands on the very next request.
func authenticateKey(ctx context.Context, keys KeyLookup, token, ip string) (context.Context, bool) {
	if keys == nil {
		return ctx, false
	}
	id, err := keys(ctx, token, ip)
	if err != nil || id == nil || id.UserID == "" || id.KeyID == "" {
		return ctx, false
	}
	ctx = context.WithValue(ctx, claimsKey, &Claims{
		UserID: id.UserID,
		Email:  id.Email,
		Role:   id.Role,
	})
	ctx = context.WithValue(ctx, machineKey, &MachinePrincipal{
		KeyID:     id.KeyID,
		Scopes:    id.Scopes,
		ServerIDs: id.ServerIDs,
		TokenHash: id.TokenHash,
	})
	return ctx, true
}

func queryTokenAllowed(r *http.Request) bool {
	return TicketPathAllowed(r.URL.Path)
}

// TicketPathAllowed reports whether a path is one of the header-less endpoints
// a ticket may open: console and metrics WebSockets, the notification stream,
// and file and backup downloads.
func TicketPathAllowed(path string) bool {
	return strings.HasSuffix(path, "/console") ||
		strings.HasSuffix(path, "/metrics") ||
		strings.HasSuffix(path, "/notifications/stream") ||
		strings.HasSuffix(path, "/files/download") ||
		strings.HasSuffix(path, "/download") && strings.Contains(path, "/backups/")
}

// remoteIP is the caller's address for usage metadata. It reads RemoteAddr
// rather than X-Forwarded-For directly: the router's TrustedProxy middleware
// has already folded a trusted forwarding header into RemoteAddr and stripped
// spoofed ones, so this cannot be forged by an untrusted peer.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearerFromHeader(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFrom(r.Context())
		// A machine key never holds administrative authority, however
		// privileged its owner is: the key's bounds are the ceiling.
		if MachineFrom(r.Context()) != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		if claims == nil || claims.Role != "admin" {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ClaimsFrom(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

// writeUnauth refuses a request. code tells a client what to do about it:
// token_expired means refresh and retry, session_revoked means sign in again.
func writeUnauth(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","code":"` + code + `"}`))
}

// WithClaims attaches user claims to a context. It authenticates nothing: it is
// for code that has already established who is acting (tests, and internal
// invocations of handlers) and needs the handlers' usual view of the caller.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}
