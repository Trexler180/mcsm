package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func Auth(token string) func(http.Handler) http.Handler {
	expected := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			presented := strings.TrimPrefix(auth, "Bearer ")
			// Constant-time compare so the token can't be recovered byte-by-byte
			// from response timing. ConstantTimeCompare also requires equal length,
			// which doubles as the "Bearer " prefix / empty-token check.
			if !strings.HasPrefix(auth, "Bearer ") ||
				subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
