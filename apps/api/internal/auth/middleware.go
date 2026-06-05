package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const claimsKey ctxKey = iota

func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerFromHeader(r)
			if token == "" && queryTokenAllowed(r) {
				// Browsers can't set Authorization on a WebSocket handshake,
				// and downloads are browser navigations, so accept ?token=…
				// only for those explicit endpoints.
				token = r.URL.Query().Get("token")
			}
			if token == "" {
				writeUnauth(w)
				return
			}
			claims, err := ParseAccessToken(secret, token)
			if err != nil {
				writeUnauth(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
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
