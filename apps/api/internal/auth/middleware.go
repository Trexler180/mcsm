package auth

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type ctxKey int

const (
	claimsKey ctxKey = iota
	machineKey
)

// Middleware authenticates a request. keys may be nil, which disables machine
// access-key authentication entirely (every mcsm_pat_ bearer then 401s).
func Middleware(secret string, tickets *TicketStore, keys KeyLookup) func(http.Handler) http.Handler {
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
						writeUnauth(w)
						return
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				claims, err := ParseAccessToken(secret, token)
				if err != nil {
					writeUnauth(w)
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
				return
			}

			// Browsers can't set Authorization on a WebSocket handshake, and
			// downloads are plain navigations, so those endpoints accept a
			// short-lived, single-use ?ticket=… instead. We never accept a raw
			// JWT in the query string — that leaks through history and logs.
			// The same rule is stricter for machine keys: they are long-lived,
			// so they are header-only and no ticket is ever minted from one.
			if queryTokenAllowed(r) {
				if claims, ok := tickets.Consume(r.URL.Query().Get("ticket")); ok {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
					return
				}
			}
			writeUnauth(w)
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
	path := r.URL.Path
	return strings.HasSuffix(path, "/console") ||
		strings.HasSuffix(path, "/metrics") ||
		strings.HasSuffix(path, "/notifications/stream") ||
		strings.HasSuffix(path, "/files/download")
}

// remoteIP is the caller's address for usage metadata. It reads RemoteAddr
// rather than X-Forwarded-For directly: the router's TrustedProxy + RealIP pair
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

func writeUnauth(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"unauthorized"}`))
}
