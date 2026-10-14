package handlers

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mcsm/agent/internal/serverlogs"
)

// Log files and the problems in them. These read the server's own logs/
// directory through FileHandlers' registered-directory lookup.

const (
	defaultLogTail = 1000
	maxLogTail     = 10000
	maxProblems    = 200
)

// ListLogs lists the server's log files, newest first.
func (h *FileHandlers) ListLogs(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	files, err := serverlogs.List(base)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list logs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// ReadLog returns the last lines of one log file (?tail=N, default 1000).
func (h *FileHandlers) ReadLog(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	n := defaultLogTail
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err = strconv.Atoi(v); err != nil || n < 1 || n > maxLogTail {
			writeError(w, http.StatusBadRequest, "tail must be between 1 and 10000")
			return
		}
	}
	name := chi.URLParam(r, "name")
	lines, truncated, err := serverlogs.Tail(base, name, n)
	if !writeLogErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "lines": lines, "truncated": truncated})
}

// LogProblems groups the warnings and errors in one log file (?file=, default
// latest.log), errors first, then by how often they occurred.
func (h *FileHandlers) LogProblems(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	name := r.URL.Query().Get("file")
	if name == "" {
		name = "latest.log"
	}
	problems, err := serverlogs.Problems(base, name, maxProblems)
	if !writeLogErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": name, "problems": problems})
}

// writeLogErr answers a failed log read; it reports whether to carry on.
func writeLogErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, serverlogs.ErrBadName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such log file")
	default:
		writeError(w, http.StatusInternalServerError, "read log: "+err.Error())
	}
	return false
}
