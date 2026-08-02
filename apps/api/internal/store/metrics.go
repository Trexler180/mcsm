package store

import (
	"context"
	"database/sql"
	"errors"
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
	// Helper-mod vitals. nil (JSON null) means no mod data covered this point —
	// distinct from a real zero, so charts draw a gap instead of a cliff.
	TPS     *float64 `json:"tps"`
	MSPTAvg *float64 `json:"mspt_avg"`
	MSPTP95 *float64 `json:"mspt_p95"`
}

// InsertServerMetric records one resource sample. The (server_id, ts) primary
// key makes a same-second re-insert a no-op instead of an error. tps/msptAvg/
// msptP95 may be nil when the helper mod isn't linked; they are stored as NULL
// so aggregates skip them rather than counting them as zero.
func (s *Store) InsertServerMetric(ctx context.Context, serverID string, ts time.Time, cpuPercent float64, ramUsedMB, ramTotalMB int64, players int, tps, msptAvg, msptP95 *float64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO server_metrics
			(server_id, ts, cpu_percent, ram_used_mb, ram_total_mb, players, tps, mspt_avg, mspt_p95)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, ts.Unix(), cpuPercent, ramUsedMB, ramTotalMB, players, tps, msptAvg, msptP95)
	return err
}

// LatestServerPlayers returns the most recent sampled player count and its
// timestamp (unix seconds). Zero timestamp when the server has no samples.
// Callers judge freshness themselves — samples land about once a minute while
// the server runs, so anything older than a few minutes is stale.
func (s *Store) LatestServerPlayers(ctx context.Context, serverID string) (players int, ts int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT players, ts FROM server_metrics WHERE server_id = ? ORDER BY ts DESC LIMIT 1`,
		serverID,
	).Scan(&players, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return players, ts, err
}

// ServerMetricsHistory returns samples for a server since `since`, averaged
// into fixed buckets so any window returns a bounded number of points.
// bucketSeconds <= 0 returns raw samples.
func (s *Store) ServerMetricsHistory(ctx context.Context, serverID string, since time.Time, bucketSeconds int64) ([]MetricPoint, error) {
	q := `
		SELECT ts, cpu_percent, ram_used_mb, ram_total_mb, players,
		       tps, mspt_avg, mspt_p95
		FROM server_metrics
		WHERE server_id = ? AND ts >= ?
		ORDER BY ts`
	args := []any{serverID, since.Unix()}
	if bucketSeconds > 0 {
		// AVG/MAX ignore NULL vitals and return NULL when a bucket has none, so
		// unlinked samples neither drag the mean down nor fake a zero.
		q = `
		SELECT (ts / ?) * ? AS bucket,
		       AVG(cpu_percent),
		       CAST(AVG(ram_used_mb) AS INTEGER),
		       MAX(ram_total_mb),
		       MAX(players),
		       AVG(tps),
		       AVG(mspt_avg),
		       MAX(mspt_p95)
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
		if err := rows.Scan(&p.TS, &p.CPUPercent, &p.RAMUsedMB, &p.RAMTotalMB, &p.Players,
			&p.TPS, &p.MSPTAvg, &p.MSPTP95); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// PruneServerMetrics deletes samples older than the retention cutoff. Callers
// must roll up first (RollupServerMetricsHourly) so pruning only drops raw
// resolution, never history.
func (s *Store) PruneServerMetrics(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM server_metrics WHERE ts < ?`, olderThan.Unix())
	return err
}

// RollupServerMetricsHourly folds raw samples into server_metrics_hourly for
// every complete hour before `upTo`, across all servers. It recomputes each
// hour still covered by raw data (REPLACE), which makes the sweep idempotent;
// hours whose raw samples were already pruned have no rows to recompute from
// and keep their stored rollup untouched.
func (s *Store) RollupServerMetricsHourly(ctx context.Context, upTo time.Time) error {
	hourStart := upTo.Unix() / 3600 * 3600
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO server_metrics_hourly
			(server_id, ts, cpu_avg, cpu_max, ram_avg_mb, ram_max_mb,
			 ram_total_mb, players_avg, players_max, samples,
			 tps_avg, tps_min, mspt_avg, mspt_p95_max, vitals_samples)
		SELECT server_id,
		       (ts / 3600) * 3600 AS hour,
		       AVG(cpu_percent),
		       MAX(cpu_percent),
		       CAST(AVG(ram_used_mb) AS INTEGER),
		       MAX(ram_used_mb),
		       MAX(ram_total_mb),
		       AVG(players),
		       MAX(players),
		       COUNT(*),
		       AVG(tps),
		       MIN(tps),
		       AVG(mspt_avg),
		       MAX(mspt_p95),
		       COUNT(tps)
		FROM server_metrics
		WHERE ts < ?
		GROUP BY server_id, hour`, hourStart)
	return err
}

// ServerMetricsHistoryHourly returns history from the forever-kept hourly
// rollups, bucket-combined so long windows stay bounded. Averages are weighted
// by each hour's sample count; players reports the bucket peak so short spikes
// survive coarse buckets. Vitals are weighted by vitals_samples instead — the
// count of samples that actually carried mod data — and a bucket with none
// yields NULL (nil) rather than 0.
func (s *Store) ServerMetricsHistoryHourly(ctx context.Context, serverID string, since time.Time, bucketSeconds int64) ([]MetricPoint, error) {
	if bucketSeconds < 3600 {
		bucketSeconds = 3600
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT (ts / ?) * ? AS bucket,
		       SUM(cpu_avg * samples) / SUM(samples),
		       CAST(SUM(ram_avg_mb * samples) / SUM(samples) AS INTEGER),
		       MAX(ram_total_mb),
		       MAX(players_max),
		       SUM(tps_avg * vitals_samples) / NULLIF(SUM(vitals_samples), 0),
		       SUM(mspt_avg * vitals_samples) / NULLIF(SUM(vitals_samples), 0),
		       MAX(mspt_p95_max)
		FROM server_metrics_hourly
		WHERE server_id = ? AND ts >= ? AND samples > 0
		GROUP BY bucket
		ORDER BY bucket`,
		bucketSeconds, bucketSeconds, serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]MetricPoint, 0, 256)
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.TS, &p.CPUPercent, &p.RAMUsedMB, &p.RAMTotalMB, &p.Players,
			&p.TPS, &p.MSPTAvg, &p.MSPTP95); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// ServerMetricsDataSince returns the timestamp of the oldest stored metric for
// a server (rollup or raw), or zero time when none exists. It anchors the
// stats page's "all time" window.
func (s *Store) ServerMetricsDataSince(ctx context.Context, serverID string) (time.Time, error) {
	var ts *int64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(ts) FROM (
			SELECT MIN(ts) AS ts FROM server_metrics_hourly WHERE server_id = ?
			UNION ALL
			SELECT MIN(ts) AS ts FROM server_metrics WHERE server_id = ?
		) WHERE ts IS NOT NULL`, serverID, serverID).Scan(&ts)
	if err != nil || ts == nil {
		return time.Time{}, err
	}
	return time.Unix(*ts, 0), nil
}
