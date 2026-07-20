package store

import (
	"context"
	"time"
)

// This file reads the server_uptime segments that updateServerStatus maintains
// (see servers.go): one row per online stretch, ended_at NULL while the server
// is still up. Because segments come from status transitions the panel
// observed, an API outage hides nothing that stayed up (the segment simply
// stays open — the Minecraft process runs independently of the panel) but a
// crash during the outage is only closed, late, on the first poll after boot.

// UptimeSegment is one online stretch, clipped to nothing — callers clamp.
type UptimeSegment struct {
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`   // 0 = still online
	EndReason string `json:"end_reason,omitempty"` // "stop" | "crash" | "" while open
}

// UptimeReport aggregates a server's availability over a window for the stats
// endpoint. WindowSeconds is the denominator actually used: the requested
// window clipped to when tracking began, so a server predating the feature
// isn't billed downtime for history nobody recorded.
type UptimeReport struct {
	TrackedSince    int64           `json:"tracked_since"` // 0 = no segments ever
	WindowSeconds   int64           `json:"window_seconds"`
	UptimeSeconds   int64           `json:"uptime_seconds"`
	AvailabilityPct float64         `json:"availability_pct"`
	Stops           int             `json:"stops"`   // clean stops ended inside the window
	Crashes         int             `json:"crashes"` // crash-attributed ends inside the window
	OnlineSince     int64           `json:"online_since,omitempty"` // 0 = offline now
	LongestSeconds  int64           `json:"longest_uptime_seconds"`
	Segments        []UptimeSegment `json:"segments"`
}

// UptimeReport builds the availability block for [since, now]: the overlapping
// segments plus totals derived from them in one pass.
func (s *Store) UptimeReport(ctx context.Context, serverID string, since, now time.Time) (*UptimeReport, error) {
	sinceU, nowU := since.Unix(), now.Unix()
	out := &UptimeReport{Segments: []UptimeSegment{}}

	var tracked *int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MIN(started_at) FROM server_uptime WHERE server_id = ?`, serverID,
	).Scan(&tracked); err != nil {
		return nil, err
	}
	if tracked == nil {
		return out, nil // never tracked: all-zero report, UI shows "no data yet"
	}
	out.TrackedSince = *tracked

	rows, err := s.db.QueryContext(ctx, `
		SELECT started_at, COALESCE(ended_at, 0), end_reason
		FROM server_uptime
		WHERE server_id = ?
		  AND (ended_at IS NULL OR ended_at >= ?)
		  AND started_at <= ?
		ORDER BY started_at`, serverID, sinceU, nowU)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var seg UptimeSegment
		if err := rows.Scan(&seg.StartedAt, &seg.EndedAt, &seg.EndReason); err != nil {
			return nil, err
		}
		out.Segments = append(out.Segments, seg)

		// Clamped in-window uptime.
		start, end := seg.StartedAt, seg.EndedAt
		if end == 0 {
			end = nowU
		}
		cs, ce := max(start, sinceU), min(end, nowU)
		if ce > cs {
			out.UptimeSeconds += ce - cs
		}
		// Longest stretch uses the segment's real (unclamped) duration — "the
		// server once ran 12 days straight" stays true in a 7d window.
		if d := end - start; d > out.LongestSeconds {
			out.LongestSeconds = d
		}
		if seg.EndedAt == 0 {
			out.OnlineSince = seg.StartedAt
		} else if seg.EndedAt >= sinceU && seg.EndedAt <= nowU {
			if seg.EndReason == "crash" {
				out.Crashes++
			} else {
				out.Stops++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	windowStart := max(sinceU, out.TrackedSince)
	if out.WindowSeconds = nowU - windowStart; out.WindowSeconds < 0 {
		out.WindowSeconds = 0
	}
	if out.WindowSeconds > 0 {
		out.AvailabilityPct = float64(out.UptimeSeconds) / float64(out.WindowSeconds) * 100
		if out.AvailabilityPct > 100 {
			out.AvailabilityPct = 100
		}
	}
	return out, nil
}

// OnlineSince returns the open uptime segment's start for a server, zero when
// none is open. Used to decorate server payloads with "up since".
func (s *Store) OnlineSince(ctx context.Context, serverID string) (int64, error) {
	var ts *int64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(started_at) FROM server_uptime WHERE server_id = ? AND ended_at IS NULL`,
		serverID,
	).Scan(&ts)
	if err != nil || ts == nil {
		return 0, err
	}
	return *ts, nil
}
