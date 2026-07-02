package handlers

import (
	"net/http"
	"time"
)

// Version identifies the running build (git revision or "dev"). Set once from
// main at startup; read-only afterwards.
var Version = "dev"

func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": Version,
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
