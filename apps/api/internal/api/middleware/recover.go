package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/mcsm/api/internal/api/apierr"
)

// Recoverer turns a panic in any later handler into a logged 500. It logs with
// the request id, so a panic can be matched to the request that caused it, and
// answers in the API's JSON error shape rather than chi's plain text.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is how a handler deliberately aborts a
			// response; re-panic so net/http handles it as intended.
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			slog.Error("panic serving request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			// A WebSocket that already hijacked the connection cannot be
			// written to; the header write below is then a no-op.
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "internal error")
		}()
		next.ServeHTTP(w, r)
	})
}
