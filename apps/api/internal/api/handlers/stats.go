package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// Stats returns the aggregate block behind the per-server stats page: headline
// totals, the player leaderboard, a weekday×hour activity heatmap, and a
// per-day series. ?days=N selects the window (0 = all time, anchored at the
// oldest stored data). ?tz_offset=M is the client's UTC offset in minutes
// (JavaScript's -getTimezoneOffset()), so heatmap and daily grouping follow the
// viewer's local calendar rather than UTC.
func (h *ServerHandlers) Stats(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 365*10 {
			writeError(w, http.StatusBadRequest, "days must be 0 (all time) to 3650")
			return
		}
		days = n
	}

	tzOffset := 0
	if v := r.URL.Query().Get("tz_offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < -14*60 || n > 14*60 {
			writeError(w, http.StatusBadRequest, "tz_offset must be minutes within ±840")
			return
		}
		tzOffset = n
	}

	now := time.Now()
	dataSince, err := h.store.StatsDataSince(r.Context(), serverID)
	if err != nil {
		writeServerError(w, r, "stats", err)
		return
	}

	since := now.AddDate(0, 0, -days)
	if days == 0 {
		since = dataSince
		if since.IsZero() {
			since = now.AddDate(0, 0, -1)
		}
	}

	summary, err := h.store.StatsSummary(r.Context(), serverID, since, now)
	if err != nil {
		writeServerError(w, r, "stats summary", err)
		return
	}
	top, err := h.store.TopPlayers(r.Context(), serverID, since, now, 25)
	if err != nil {
		writeServerError(w, r, "stats top players", err)
		return
	}
	activity, err := h.store.ActivityHeatmap(r.Context(), serverID, since, tzOffset*60)
	if err != nil {
		writeServerError(w, r, "stats activity", err)
		return
	}
	daily, err := h.store.DailyStats(r.Context(), serverID, since, now, tzOffset*60)
	if err != nil {
		writeServerError(w, r, "stats daily", err)
		return
	}
	uptime, err := h.store.UptimeReport(r.Context(), serverID, since, now)
	if err != nil {
		writeServerError(w, r, "stats uptime", err)
		return
	}

	var dataSinceUnix int64
	if !dataSince.IsZero() {
		dataSinceUnix = dataSince.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days":        days,
		"since":       since.Unix(),
		"data_since":  dataSinceUnix,
		"summary":     summary,
		"top_players": top,
		"activity":    activity,
		"daily":       daily,
		"uptime":      uptime,
	})
}
