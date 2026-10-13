package handlers

import (
	"net"
	"net/http"
	"time"
)

// Version identifies the running build (git revision or "dev"). Set once from
// main at startup; read-only afterwards.
var Version = "dev"

// Health answers liveness probes. It is public, so it does not name the build
// to the internet: the version is included only for a direct request from this
// machine (the deploy script's check), never for one that came through the
// reverse proxy.
func Health(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{"status": "ok"}
	if localDirect(r) {
		out["version"] = Version
	}
	writeJSON(w, http.StatusOK, out)
}

// localDirect reports whether a request came from this machine without passing
// through a proxy.
func localDirect(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Proto"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

// System describes the running panel to a signed-in user: its build and its
// clock (scheduled tasks fire on this clock).
func System(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	zone, offset := now.Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        Version,
		"now":            now.Format(time.RFC3339),
		"zone":           zone,
		"offset_seconds": offset,
	})
}

// Time reports the panel's current wall-clock time and timezone. Scheduled
// tasks fire on the API process's clock (the cron scheduler uses its local
// timezone), so the UI shows this alongside cron schedules to remove the "is
// 4am my time or the server's?" ambiguity.
func Time(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	zone, offset := now.Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"now":            now.Format(time.RFC3339),
		"zone":           zone,   // abbreviation, e.g. "UTC" / "EST"
		"offset_seconds": offset, // east-of-UTC seconds, e.g. -18000
	})
}
