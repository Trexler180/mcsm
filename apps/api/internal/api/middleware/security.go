package middleware

import (
	"net/http"
	"strings"
)

// SecurityHeaders sets defensive response headers. The API only ever returns
// JSON or file streams (downloads are forced via Content-Disposition), so a
// lock-everything-down CSP is safe and backstops any future HTML rendering.
// HSTS is only emitted over HTTPS so a plain-HTTP dev setup isn't pinned.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if requestIsHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// MaxBodyBytes caps the size of request bodies the API will read, so a single
// large POST/PUT can't exhaust memory. Multipart uploads are proxied straight
// through to the agent (which enforces its own multipart cap), so this limit
// only applies to non-multipart bodies — JSON, text file content, etc.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "multipart/") {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
