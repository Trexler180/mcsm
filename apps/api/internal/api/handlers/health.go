package handlers

import "net/http"

// Version identifies the running build (git revision or "dev"). Set once from
// main at startup; read-only afterwards.
var Version = "dev"

func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": Version,
	})
}
