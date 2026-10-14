package handlers

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// The server's own log files (logs/latest.log and the rotated .log.gz files)
// and the warnings and errors grouped from them, for the console's "Log files"
// and "Problems" views. They hold what the live console shows, so they sit
// behind the same console permission.

// ListLogs lists the server's log files, newest first.
func (h *FileHandlers) ListLogs(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/logs")
}

// ReadLog returns the last lines of one log file (?tail=N, up to 10000).
func (h *FileHandlers) ReadLog(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/logs/"+url.PathEscape(chi.URLParam(r, "name")))
}

// LogProblems groups the warnings and errors of one log file (?file=, default
// latest.log), errors first, then by frequency.
func (h *FileHandlers) LogProblems(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/logs/problems")
}
