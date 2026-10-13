package store

import (
	"context"
	"os"
	"strconv"
	"time"
)

// History retention. Raw metrics have their own retention with hourly
// rollups; these are the other tables that only ever grew.
const (
	notificationRetention = 90 * 24 * time.Hour
	deliveryRetention     = 30 * 24 * time.Hour
	logEventRetention     = 90 * 24 * time.Hour
	operationRetention    = 180 * 24 * time.Hour
	taskRunRetention      = 90 * 24 * time.Hour
	runRetention          = 365 * 24 * time.Hour
	sessionRetention      = 365 * 24 * time.Hour
	// defaultAuditRetentionDays keeps a year of audit history. Set
	// AUDIT_RETENTION_DAYS to change it; 0 keeps everything.
	defaultAuditRetentionDays = 365
)

// AuditRetention is how long audit rows are kept, or 0 for forever.
func AuditRetention() time.Duration {
	days := defaultAuditRetentionDays
	if v := os.Getenv("AUDIT_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}

// PruneHistory deletes rows older than their retention window and returns how
// many went. Only finished things are pruned: an unread notification is kept
// as long as a read one, a pending delivery is never pruned, and a running
// operation or run is never touched.
func (s *Store) PruneHistory(ctx context.Context, now time.Time) (int64, error) {
	type rule struct {
		q      string
		cutoff time.Time
		unix   bool
	}
	rules := []rule{
		{`DELETE FROM notifications WHERE created_at < ?`, now.Add(-notificationRetention), false},
		{`DELETE FROM notification_deliveries WHERE status IN ('sent','failed','skipped') AND updated_at < ?`, now.Add(-deliveryRetention), false},
		{`DELETE FROM server_log_events WHERE created_at < ?`, now.Add(-logEventRetention), false},
		{`DELETE FROM operations WHERE status != 'running' AND started_at < ?`, now.Add(-operationRetention), true},
		{`DELETE FROM scheduled_task_runs WHERE status != 'running' AND started_at < ?`, now.Add(-taskRunRetention), true},
		{`DELETE FROM mod_update_runs WHERE status != 'running' AND started_at < ?`, now.Add(-runRetention), false},
		{`DELETE FROM server_version_migrations WHERE status != 'running' AND started_at < ?`, now.Add(-runRetention), false},
		{`DELETE FROM player_sessions WHERE ended_at IS NOT NULL AND started_at < ?`, now.Add(-sessionRetention), false},
	}
	if keep := AuditRetention(); keep > 0 {
		rules = append(rules, rule{`DELETE FROM audit_log WHERE created_at < ?`, now.Add(-keep), false})
	}
	var total int64
	for _, r := range rules {
		var arg any = sqliteTime(r.cutoff)
		if r.unix {
			arg = r.cutoff.Unix()
		}
		res, err := s.db.ExecContext(ctx, r.q, arg)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
