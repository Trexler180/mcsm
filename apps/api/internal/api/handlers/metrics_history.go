package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// metricsHistoryMaxPoints bounds the response size: the bucket width is chosen
// so any window returns at most about this many points.
const metricsHistoryMaxPoints = 360

// MetricsHistory returns bucket-averaged resource samples for a server.
// ?hours=N selects the window (default 24, capped at the 30-day retention).
func (h *ServerHandlers) MetricsHistory(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 24*30 {
			writeError(w, http.StatusBadRequest, "hours must be between 1 and 720")
			return
		}
		hours = n
	}

	window := time.Duration(hours) * time.Hour
	bucket := int64(window.Seconds()) / metricsHistoryMaxPoints
	if bucket < 60 {
		bucket = 60 // samples arrive about once a minute; finer buckets are noise
	}

	points, err := h.store.ServerMetricsHistory(r.Context(), serverID, time.Now().Add(-window), bucket)
	if err != nil {
		writeServerError(w, r, "metrics history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hours":          hours,
		"bucket_seconds": bucket,
		"points":         points,
	})
}
