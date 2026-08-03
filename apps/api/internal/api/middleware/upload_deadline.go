package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// UploadDeadline replaces the server's whole-request read deadline for multipart
// bodies.
//
// http.Server.ReadTimeout is a deadline on reading the *entire* request, body
// included, and it is never extended as the body arrives. With a 30s
// ReadTimeout, any upload that takes longer than 30s to reach us is severed
// mid-stream — a 133 MB world on a 9 Mbps home connection dies around a quarter
// of the way through. Worse, the failure surfaces at the far end of the proxy:
// the severed read fails the API's outbound request to the agent, which reports
// as "agent unreachable" while the agent is perfectly healthy.
//
// The deadline is replaced rather than cleared so a stalled or slow-dripped
// upload still can't hold a connection open forever; d should match the budget
// the upload handlers give themselves. Header reads are untouched —
// ReadHeaderTimeout still bounds the slowloris case, which is what that knob is
// actually for.
//
// Install this before any middleware that wraps the ResponseWriter, or make sure
// the wrappers implement Unwrap; http.ResponseController needs to reach the
// underlying connection to move the deadline.
func UploadDeadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(d)); err != nil {
					slog.Warn("upload deadline: could not move read deadline; large uploads will be cut off by ReadTimeout",
						"err", err, "path", r.URL.Path)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
