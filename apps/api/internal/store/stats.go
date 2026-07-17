package store

import (
	"context"
	"time"
)

// This file backs the per-server stats page: aggregates over player_sessions
// (kept forever, one row per visit) and the server metrics tables (raw
// one-minute samples for 30 days + hourly rollups kept forever).
//
// Timestamps in player_sessions are DATETIME text in the sqlTime format, so
// every comparison converts through CAST(strftime('%s', …) AS INTEGER) —
// without the cast SQLite would compare TEXT against INTEGER by type order,
// which is always true.

// startS / endS are the session start / clamped end as unix-second SQL
// expressions. An open session counts up to "now" (bound by the caller).
const (
	sessStartS = `CAST(strftime('%s', started_at) AS INTEGER)`
	sessEndS   = `MIN(COALESCE(CAST(strftime('%s', ended_at) AS INTEGER), ?), ?)`
)

// StatsSummary is the headline aggregate block of the stats endpoint.
type StatsSummary struct {
	UniquePlayers         int    `json:"unique_players"`
	TotalJoins            int    `json:"total_joins"`
	PlaytimeSeconds       int64  `json:"playtime_seconds"`
	PeakPlayers           int    `json:"peak_players"`
	PeakTS                int64  `json:"peak_ts,omitempty"`
	UptimeSeconds         int64  `json:"uptime_seconds"`
	LongestSessionSeconds int64  `json:"longest_session_seconds"`
	LongestSessionPlayer  string `json:"longest_session_player,omitempty"`
}

// TopPlayer is one row of the stats page leaderboard.
type TopPlayer struct {
	Name            string `json:"name"`
	UUID            string `json:"uuid,omitempty"`
	PlaytimeSeconds int64  `json:"playtime_seconds"`
	Joins           int    `json:"joins"`
	LastSeen        int64  `json:"last_seen"`
	Online          bool   `json:"online"`
}

// ActivityCell is one weekday × hour cell of the activity heatmap, in the
// requester's timezone.
type ActivityCell struct {
	Dow        int     `json:"dow"` // 0 = Sunday, matching strftime('%w')
	Hour       int     `json:"hour"`
	AvgPlayers float64 `json:"avg_players"`
	MaxPlayers int     `json:"max_players"`
}

// DailyStat is one local-calendar-day row of the stats page daily series.
type DailyStat struct {
	Date            string `json:"date"` // YYYY-MM-DD in the requester's timezone
	UniquePlayers   int    `json:"unique_players"`
	Joins           int    `json:"joins"`
	PlaytimeSeconds int64  `json:"playtime_seconds"`
	PeakPlayers     int    `json:"peak_players"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
}

// StatsDataSince returns the earliest stored data point for a server across
// metrics (rollup + raw) and player sessions — the anchor for the stats page's
// "all time" window. Zero time when the server has no history at all.
func (s *Store) StatsDataSince(ctx context.Context, serverID string) (time.Time, error) {
	var ts *int64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(ts) FROM (
			SELECT MIN(ts) AS ts FROM server_metrics_hourly WHERE server_id = ?
			UNION ALL
			SELECT MIN(ts) AS ts FROM server_metrics WHERE server_id = ?
			UNION ALL
			SELECT MIN(CAST(strftime('%s', started_at) AS INTEGER)) AS ts
			FROM player_sessions WHERE server_id = ?
		) WHERE ts IS NOT NULL`, serverID, serverID, serverID).Scan(&ts)
	if err != nil || ts == nil {
		return time.Time{}, err
	}
	return time.Unix(*ts, 0), nil
}

// StatsSummary aggregates a server's window: player totals from sessions
// (clamped to the window so a session straddling the boundary only counts its
// in-window part), peak players and uptime from the metrics tables.
func (s *Store) StatsSummary(ctx context.Context, serverID string, since, now time.Time) (*StatsSummary, error) {
	out := &StatsSummary{}
	sinceU, nowU := since.Unix(), now.Unix()

	// Sessions overlapping the window: totals + the longest single session.
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT lower(player_name)),
		       COALESCE(SUM(CASE WHEN `+sessStartS+` >= ? THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(MAX(0, `+sessEndS+` - MAX(`+sessStartS+`, ?))), 0)
		FROM player_sessions
		WHERE server_id = ?
		  AND (ended_at IS NULL OR CAST(strftime('%s', ended_at) AS INTEGER) >= ?)
		  AND `+sessStartS+` <= ?`,
		sinceU, nowU, nowU, sinceU, serverID, sinceU, nowU,
	).Scan(&out.UniquePlayers, &out.TotalJoins, &out.PlaytimeSeconds)
	if err != nil {
		return nil, err
	}

	var longestName *string
	var longest *int64
	err = s.db.QueryRowContext(ctx, `
		SELECT player_name, `+sessEndS+` - `+sessStartS+` AS dur
		FROM player_sessions
		WHERE server_id = ? AND `+sessStartS+` >= ?
		ORDER BY dur DESC LIMIT 1`,
		nowU, nowU, serverID, sinceU,
	).Scan(&longestName, &longest)
	if err == nil && longest != nil {
		out.LongestSessionSeconds = *longest
		if longestName != nil {
			out.LongestSessionPlayer = *longestName
		}
	} // no rows: leave zeroes

	// Peak: max concurrent players across rollups and raw samples. The two
	// overlap inside raw retention; MAX makes the union harmless.
	var peakTS, peak *int64
	err = s.db.QueryRowContext(ctx, `
		SELECT ts, players FROM (
			SELECT ts, players_max AS players FROM server_metrics_hourly WHERE server_id = ? AND ts >= ?
			UNION ALL
			SELECT ts, players FROM server_metrics WHERE server_id = ? AND ts >= ?
		) ORDER BY players DESC, ts ASC LIMIT 1`,
		serverID, sinceU, serverID, sinceU,
	).Scan(&peakTS, &peak)
	if err == nil && peak != nil {
		out.PeakPlayers = int(*peak)
		if peakTS != nil {
			out.PeakTS = *peakTS
		}
	}

	// Uptime: every stored sample ≈ one minute of the server running. Rollups
	// carry their sample count; raw rows past the last rolled-up hour fill in
	// the recent tail without double counting.
	var rolledSamples int64
	var lastRolled *int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(samples), 0), MAX(ts)
		FROM server_metrics_hourly WHERE server_id = ? AND ts >= ?`,
		serverID, sinceU,
	).Scan(&rolledSamples, &lastRolled); err != nil {
		return nil, err
	}
	rawFrom := sinceU
	if lastRolled != nil && *lastRolled+3600 > rawFrom {
		rawFrom = *lastRolled + 3600
	}
	var rawSamples int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM server_metrics WHERE server_id = ? AND ts >= ?`,
		serverID, rawFrom,
	).Scan(&rawSamples); err != nil {
		return nil, err
	}
	out.UptimeSeconds = (rolledSamples + rawSamples) * 60

	return out, nil
}

// TopPlayers ranks the window's players by clamped in-window playtime. Names
// group case-insensitively; the display name comes from an arbitrary session
// row (variants differ only in case) and the uuid from MAX(), which prefers
// any non-empty value over ''.
func (s *Store) TopPlayers(ctx context.Context, serverID string, since, now time.Time, limit int) ([]TopPlayer, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	sinceU, nowU := since.Unix(), now.Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT player_name,
		       MAX(`+sessStartS+`) AS last_start,
		       MAX(player_uuid),
		       SUM(MAX(0, `+sessEndS+` - MAX(`+sessStartS+`, ?))),
		       SUM(CASE WHEN `+sessStartS+` >= ? THEN 1 ELSE 0 END),
		       MAX(COALESCE(CAST(strftime('%s', ended_at) AS INTEGER), ?)),
		       MAX(CASE WHEN ended_at IS NULL THEN 1 ELSE 0 END)
		FROM player_sessions
		WHERE server_id = ?
		  AND (ended_at IS NULL OR CAST(strftime('%s', ended_at) AS INTEGER) >= ?)
		  AND `+sessStartS+` <= ?
		GROUP BY lower(player_name)
		ORDER BY 4 DESC
		LIMIT ?`,
		nowU, nowU, sinceU, sinceU, nowU, serverID, sinceU, nowU, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TopPlayer, 0, limit)
	for rows.Next() {
		var p TopPlayer
		var lastStart int64
		var online int
		if err := rows.Scan(&p.Name, &lastStart, &p.UUID, &p.PlaytimeSeconds, &p.Joins, &p.LastSeen, &online); err != nil {
			return nil, err
		}
		p.Online = online == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActivityHeatmap averages concurrent players into weekday × hour cells from
// the hourly rollups, shifted into the requester's timezone. Cells with no
// recorded uptime are simply absent.
func (s *Store) ActivityHeatmap(ctx context.Context, serverID string, since time.Time, tzOffsetSeconds int) ([]ActivityCell, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT CAST(strftime('%w', ts + ?, 'unixepoch') AS INTEGER) AS dow,
		       CAST(strftime('%H', ts + ?, 'unixepoch') AS INTEGER) AS hr,
		       SUM(players_avg * samples) / SUM(samples),
		       MAX(players_max)
		FROM server_metrics_hourly
		WHERE server_id = ? AND ts >= ? AND samples > 0
		GROUP BY dow, hr
		ORDER BY dow, hr`,
		tzOffsetSeconds, tzOffsetSeconds, serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ActivityCell, 0, 168)
	for rows.Next() {
		var c ActivityCell
		if err := rows.Scan(&c.Dow, &c.Hour, &c.AvgPlayers, &c.MaxPlayers); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DailyStats builds the per-local-day series: session-derived counts (a
// session belongs to the day it started) merged with metric-derived peaks and
// uptime. Days only one source knows about still appear.
func (s *Store) DailyStats(ctx context.Context, serverID string, since, now time.Time, tzOffsetSeconds int) ([]DailyStat, error) {
	sinceU, nowU := since.Unix(), now.Unix()
	byDay := map[string]*DailyStat{}
	order := []string{}
	day := func(key string) *DailyStat {
		if d, ok := byDay[key]; ok {
			return d
		}
		d := &DailyStat{Date: key}
		byDay[key] = d
		order = append(order, key)
		return d
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT date(`+sessStartS+` + ?, 'unixepoch') AS day,
		       COUNT(DISTINCT lower(player_name)),
		       COUNT(*),
		       COALESCE(SUM(MAX(0, `+sessEndS+` - `+sessStartS+`)), 0)
		FROM player_sessions
		WHERE server_id = ? AND `+sessStartS+` >= ?
		GROUP BY day ORDER BY day`,
		tzOffsetSeconds, nowU, nowU, serverID, sinceU)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var uniq, joins int
		var playtime int64
		if err := rows.Scan(&key, &uniq, &joins, &playtime); err != nil {
			rows.Close()
			return nil, err
		}
		d := day(key)
		d.UniquePlayers, d.Joins, d.PlaytimeSeconds = uniq, joins, playtime
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT date(ts + ?, 'unixepoch') AS day, MAX(players_max), SUM(samples)
		FROM server_metrics_hourly
		WHERE server_id = ? AND ts >= ?
		GROUP BY day ORDER BY day`,
		tzOffsetSeconds, serverID, sinceU)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var peak int
		var samples int64
		if err := rows.Scan(&key, &peak, &samples); err != nil {
			rows.Close()
			return nil, err
		}
		d := day(key)
		d.PeakPlayers = peak
		d.UptimeSeconds = samples * 60
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Merge order: session days arrived sorted, metric days too, but the two
	// interleave — sort the union.
	out := make([]DailyStat, 0, len(order))
	for _, key := range order {
		out = append(out, *byDay[key])
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Date < out[j-1].Date; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}
