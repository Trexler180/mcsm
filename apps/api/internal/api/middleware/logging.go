package middleware

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = 0

// RequestID assigns each request a short id (honoring an inbound X-Request-ID),
// stores it in the context, and echoes it in the response header so logs on both
// sides can be correlated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"request_id", RequestIDFrom(r.Context()),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// Hijack passes through to the underlying ResponseWriter so WebSocket
// upgrades work (coder/websocket needs the raw TCP conn).
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("ResponseWriter does not support hijacking")
	}
	return h.Hijack()
}

// Flush passes through so streaming handlers (SSE, chunked responses) work.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
