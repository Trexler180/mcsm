package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const claimsKey ctxKey = iota

func Middleware(secret string, tickets *TicketStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Normal API calls carry the JWT in the Authorization header.
			if token := bearerFromHeader(r); token != "" {
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

func queryTokenAllowed(r *http.Request) bool {
	path := r.URL.Path
	return strings.HasSuffix(path, "/console") ||
		strings.HasSuffix(path, "/metrics") ||
		strings.HasSuffix(path, "/files/download")
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
