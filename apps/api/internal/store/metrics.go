package store

import (
	"context"
	"time"
)

// MetricPoint is one (possibly bucket-averaged) sample of a server's resource
// usage, as returned to the history endpoint.
type MetricPoint struct {
	TS         int64   `json:"ts"` // unix seconds (bucket start when downsampled)
	CPUPercent float64 `json:"cpu_percent"`
	RAMUsedMB  int64   `json:"ram_used_mb"`
	RAMTotalMB int64   `json:"ram_total_mb"`
	Players    int     `json:"players"`
}

// InsertServerMetric records one resource sample. The (server_id, ts) primary
// key makes a same-second re-insert a no-op instead of an error.
func (s *Store) InsertServerMetric(ctx context.Context, serverID string, ts time.Time, cpuPercent float64, ramUsedMB, ramTotalMB int64, players int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO server_metrics (server_id, ts, cpu_percent, ram_used_mb, ram_total_mb, players)
		VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, ts.Unix(), cpuPercent, ramUsedMB, ramTotalMB, players)
	return err
}

// ServerMetricsHistory returns samples for a server since `since`, averaged
// into fixed buckets so any window returns a bounded number of points.
// bucketSeconds <= 0 returns raw samples.
func (s *Store) ServerMetricsHistory(ctx context.Context, serverID string, since time.Time, bucketSeconds int64) ([]MetricPoint, error) {
	q := `
		SELECT ts, cpu_percent, ram_used_mb, ram_total_mb, players
		FROM server_metrics
		WHERE server_id = ? AND ts >= ?
		ORDER BY ts`
	args := []any{serverID, since.Unix()}
	if bucketSeconds > 0 {
		q = `
		SELECT (ts / ?) * ? AS bucket,
		       AVG(cpu_percent),
		       CAST(AVG(ram_used_mb) AS INTEGER),
		       MAX(ram_total_mb),
		       MAX(players)
		FROM server_metrics
		WHERE server_id = ? AND ts >= ?
		GROUP BY bucket
		ORDER BY bucket`
		args = []any{bucketSeconds, bucketSeconds, serverID, since.Unix()}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]MetricPoint, 0, 256)
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.TS, &p.CPUPercent, &p.RAMUsedMB, &p.RAMTotalMB, &p.Players); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// PruneServerMetrics deletes samples older than the retention cutoff.
func (s *Store) PruneServerMetrics(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM server_metrics WHERE ts < ?`, olderThan.Unix())
	return err
}
