package middleware

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// UploadDeadline replaces the server's whole-request read deadline for multipart
// bodies.
//
// http.Server.ReadTimeout is a deadline on reading the *entire* request, body
// included, and it is never extended as the body arrives. With a 30s
// ReadTimeout, any upload that takes longer than 30s to arrive is severed
// mid-stream — and since the panel streams world and file uploads straight
// through to us, that limit applies to the user's connection speed, not ours.
//
// The deadline is replaced rather than cleared so a stalled upload still can't
// hold a connection open forever; d should match the budget the API gives the
// same request. Header reads are untouched — ReadHeaderTimeout still bounds the
// slowloris case, which is what that knob is actually for.
//
// Install this before any middleware that wraps the ResponseWriter, or make sure
// the wrappers implement Unwrap; http.ResponseController needs to reach the
// underlying connection to move the deadline.
func UploadDeadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(d)); err != nil {
					log.Printf("upload deadline: could not move read deadline (%v); large uploads will be cut off by ReadTimeout", err)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
