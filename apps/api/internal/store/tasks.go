package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── Scheduled Tasks ──────────────────────────────────────────────

func (s *Store) ListTasks(ctx context.Context, serverID string) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at, created_by
		 FROM scheduled_tasks WHERE server_id=? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt, &t.CreatedBy); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// ListAllEnabledTasks returns every enabled task across all servers. The
// scheduler uses this to (re)register cron entries.
func (s *Store) ListAllEnabledTasks(ctx context.Context) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at, created_by
		 FROM scheduled_tasks WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt, &t.CreatedBy); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// UpdateTaskLastRun records when a task fired and the next scheduled time.
func (s *Store) UpdateTaskLastRun(ctx context.Context, id string, lastRun time.Time, nextRun *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET last_run=?, next_run=? WHERE id=?`,
		lastRun, nextRun, id)
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*ScheduledTask, error) {
	var t ScheduledTask
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at, created_by
		 FROM scheduled_tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt, &t.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found")
	}
	return &t, err
}

func (s *Store) CreateTask(ctx context.Context, t *ScheduledTask) (*ScheduledTask, error) {
	id := uuid.NewString()
	// Populate next_run up front so the UI shows an accurate countdown before the
	// task has ever fired. Disabled tasks have no upcoming run.
	var next *time.Time
	if t.Enabled {
		next = NextRunForCron(t.CronExpr, time.Now())
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (id, server_id, name, cron_expr, action, payload, enabled, next_run, created_by)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		id, t.ServerID, t.Name, t.CronExpr, t.Action, jsonRaw(t.Payload), t.Enabled, next, t.CreatedBy,
	)
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) UpdateTask(ctx context.Context, id string, t *ScheduledTask) error {
	// Recompute next_run: the cron expr or enabled flag may have changed. Clears
	// it (NULL) when the task is disabled.
	var next *time.Time
	if t.Enabled {
		next = NextRunForCron(t.CronExpr, time.Now())
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET name=?, cron_expr=?, action=?, payload=?, enabled=?, next_run=?, created_by=? WHERE id=?`,
		t.Name, t.CronExpr, t.Action, jsonRaw(t.Payload), t.Enabled, next, t.CreatedBy, id,
	)
	return err
}

// SetTaskNextRun updates only the cached next_run, used by the scheduler to keep
// the stored countdown fresh (e.g. after an API restart left it stale/past).
func (s *Store) SetTaskNextRun(ctx context.Context, id string, next *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET next_run=? WHERE id=?`, next, id)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id=?`, id)
	return err
}
