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

// rawMetricsWindow is how much one-minute raw history the sampler retains;
// windows beyond it are served from the forever-kept hourly rollups.
const rawMetricsWindow = 30 * 24 * time.Hour

// MetricsHistory returns bucket-averaged resource samples for a server.
// ?hours=N selects the window: up to 30 days it reads the raw one-minute
// samples, beyond that the hourly rollups (kept forever). hours=0 means "all
// time", anchored at the server's oldest stored data point.
func (h *ServerHandlers) MetricsHistory(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 24*365*10 {
			writeError(w, http.StatusBadRequest, "hours must be 0 (all time) to 87600")
			return
		}
		hours = n
	}

	now := time.Now()
	window := time.Duration(hours) * time.Hour
	if hours == 0 {
		oldest, err := h.store.ServerMetricsDataSince(r.Context(), serverID)
		if err != nil {
			writeServerError(w, r, "metrics history", err)
			return
		}
		if oldest.IsZero() {
			window = rawMetricsWindow // no data yet; any window returns nothing
		} else {
			window = now.Sub(oldest) + time.Hour
		}
		if window < time.Hour {
			window = time.Hour
		}
	}

	bucket := int64(window.Seconds()) / metricsHistoryMaxPoints
	var points any
	var err error
	if window <= rawMetricsWindow {
		if bucket < 60 {
			bucket = 60 // samples arrive about once a minute; finer buckets are noise
		}
		points, err = h.store.ServerMetricsHistory(r.Context(), serverID, now.Add(-window), bucket)
	} else {
		if bucket < 3600 {
			bucket = 3600
		}
		points, err = h.store.ServerMetricsHistoryHourly(r.Context(), serverID, now.Add(-window), bucket)
	}
	if err != nil {
		writeServerError(w, r, "metrics history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hours":          hours,
		"window_hours":   int(window / time.Hour),
		"bucket_seconds": bucket,
		"points":         points,
	})
}
