package store

import (
	"context"
	"time"
)

// Queries behind the fleet summary. Each covers every server in one statement;
// the caller narrows to what the viewer may see.

// LatestSample is a server's most recent resource sample.
type LatestSample struct {
	TS         time.Time `json:"ts"`
	CPUPercent float64   `json:"cpu_percent"`
	RAMUsedMB  int64     `json:"ram_used_mb"`
	RAMTotalMB int64     `json:"ram_total_mb"`
	Players    int       `json:"players"`
	TPS        *float64  `json:"tps"`
}

// LatestServerSamples returns each server's newest sample taken since the
// cutoff (older ones are stale and left out).
func (s *Store) LatestServerSamples(ctx context.Context, since time.Time) (map[string]LatestSample, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.server_id, m.ts, m.cpu_percent, m.ram_used_mb, m.ram_total_mb, m.players, m.tps
		FROM server_metrics m
		JOIN (SELECT server_id, MAX(ts) AS ts FROM server_metrics WHERE ts >= ? GROUP BY server_id) l
		  ON l.server_id = m.server_id AND l.ts = m.ts`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]LatestSample{}
	for rows.Next() {
		var id string
		var ts int64
		var smp LatestSample
		if err := rows.Scan(&id, &ts, &smp.CPUPercent, &smp.RAMUsedMB, &smp.RAMTotalMB, &smp.Players, &smp.TPS); err != nil {
			return nil, err
		}
		smp.TS = time.Unix(ts, 0).UTC()
		out[id] = smp
	}
	return out, rows.Err()
}

// HourlyPlayerPeaks returns, per server, the most players seen in each hour
// bucket (unix seconds, hour start) since the cutoff.
func (s *Store) HourlyPlayerPeaks(ctx context.Context, since time.Time) (map[string]map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT server_id, (ts / 3600) * 3600 AS hour, MAX(players)
		FROM server_metrics WHERE ts >= ?
		GROUP BY server_id, hour`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int64]int{}
	for rows.Next() {
		var id string
		var hour int64
		var peak int
		if err := rows.Scan(&id, &hour, &peak); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[int64]int{}
		}
		out[id][hour] = peak
	}
	return out, rows.Err()
}

// UpcomingTask is a scheduled task due to run soon.
type UpcomingTask struct {
	TaskID   string    `json:"task_id"`
	ServerID string    `json:"server_id"`
	Name     string    `json:"name"`
	Action   string    `json:"action"`
	NextRun  time.Time `json:"next_run"`
}

// UpcomingTasks lists enabled tasks due before the cutoff, soonest first.
func (s *Store) UpcomingTasks(ctx context.Context, before time.Time) ([]UpcomingTask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, server_id, name, action, next_run FROM scheduled_tasks
		WHERE enabled = 1 AND next_run IS NOT NULL AND next_run <= ?
		ORDER BY next_run`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UpcomingTask{}
	for rows.Next() {
		var t UpcomingTask
		if err := rows.Scan(&t.TaskID, &t.ServerID, &t.Name, &t.Action, &t.NextRun); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ModUpdateCheck is the result of the last mod-update check of a server.
type ModUpdateCheck struct {
	Available int       `json:"available"`
	CheckedAt time.Time `json:"checked_at"`
}

// RecordModUpdateCheck stores how many updates a check found.
func (s *Store) RecordModUpdateCheck(ctx context.Context, serverID string, available int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mod_update_checks (server_id, available, checked_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(server_id) DO UPDATE SET available = excluded.available, checked_at = excluded.checked_at`,
		serverID, available)
	return err
}

// ModUpdateChecks returns the last check of every server that has one.
func (s *Store) ModUpdateChecks(ctx context.Context) (map[string]ModUpdateCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, available, checked_at FROM mod_update_checks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ModUpdateCheck{}
	for rows.Next() {
		var id string
		var c ModUpdateCheck
		if err := rows.Scan(&id, &c.Available, &c.CheckedAt); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ModUpdateCheckFor returns a server's last mod-update check, or nil.
func (s *Store) ModUpdateCheckFor(ctx context.Context, serverID string) (*ModUpdateCheck, error) {
	var c ModUpdateCheck
	err := s.db.QueryRowContext(ctx,
		`SELECT available, checked_at FROM mod_update_checks WHERE server_id = ?`, serverID).Scan(&c.Available, &c.CheckedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
