package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// TaskRun is one firing of a scheduled task.
type TaskRun struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"task_id"`
	ServerID    string     `json:"server_id"`
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	OperationID *string    `json:"operation_id"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// StartTaskRun records that a task fired.
func (s *Store) StartTaskRun(ctx context.Context, taskID, serverID, action string) (*TaskRun, error) {
	run := &TaskRun{ID: uuid.NewString(), TaskID: taskID, ServerID: serverID, Action: action,
		Status: "running", StartedAt: time.Now().UTC()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_task_runs (id, task_id, server_id, action, status, started_at) VALUES (?,?,?,?,?,?)`,
		run.ID, taskID, serverID, action, run.Status, run.StartedAt.Unix())
	return run, err
}

// FinishTaskRun records how a run ended.
func (s *Store) FinishTaskRun(ctx context.Context, id, status, message string, operationID string) error {
	if len(message) > 1000 {
		message = message[:1000]
	}
	var op any
	if operationID != "" {
		op = operationID
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_task_runs SET status = ?, message = ?, operation_id = COALESCE(?, operation_id), finished_at = ?
		  WHERE id = ?`, status, message, op, time.Now().Unix(), id)
	return err
}

// ListTaskRuns returns runs, newest first, for one task (taskID set) or every
// task on a server.
func (s *Store) ListTaskRuns(ctx context.Context, serverID, taskID string, limit int) ([]*TaskRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, task_id, server_id, action, status, message, operation_id, started_at, finished_at
	        FROM scheduled_task_runs WHERE server_id = ?`
	args := []any{serverID}
	if taskID != "" {
		q += ` AND task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TaskRun{}
	for rows.Next() {
		var r TaskRun
		var started int64
		var finished sql.NullInt64
		if err := rows.Scan(&r.ID, &r.TaskID, &r.ServerID, &r.Action, &r.Status, &r.Message, &r.OperationID, &started, &finished); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0).UTC()
		if finished.Valid {
			t := time.Unix(finished.Int64, 0).UTC()
			r.FinishedAt = &t
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// InterruptRunningTaskRuns closes runs a previous process left running.
func (s *Store) InterruptRunningTaskRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_task_runs SET status = 'failed', message = 'the panel restarted during this run', finished_at = ?
		  WHERE status = 'running'`, time.Now().Unix())
	return err
}
