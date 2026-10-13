package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Operation statuses.
const (
	OpRunning     = "running"
	OpSucceeded   = "succeeded"
	OpFailed      = "failed"
	OpCancelled   = "cancelled"
	OpInterrupted = "interrupted"
)

// Operation is one long-running thing done to a server. See migration 032.
type Operation struct {
	ID           string          `json:"id"`
	ServerID     string          `json:"server_id"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Exclusive    bool            `json:"exclusive"`
	Trigger      string          `json:"trigger"`
	ActorUserID  *string         `json:"actor_user_id"`
	ActorKeyID   *string         `json:"actor_key_id,omitempty"`
	ActorGrantID *string         `json:"actor_grant_id,omitempty"`
	Phase        string          `json:"phase"`
	Message      string          `json:"message"`
	Progress     *float64        `json:"progress"`
	Error        string          `json:"error,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	RefID        *string         `json:"ref_id,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
}

// ErrServerBusy is returned when an exclusive operation is already running on
// the server.
type ErrServerBusy struct {
	Current *Operation
}

func (e *ErrServerBusy) Error() string {
	if e.Current == nil {
		return "another operation is in progress on this server"
	}
	return fmt.Sprintf("this server is busy: %s is in progress", strings.ReplaceAll(e.Current.Kind, "_", " "))
}

// AsServerBusy unwraps an ErrServerBusy.
func AsServerBusy(err error) (*ErrServerBusy, bool) {
	var busy *ErrServerBusy
	if errors.As(err, &busy) {
		return busy, true
	}
	return nil, false
}

const opCols = `id, server_id, kind, status, exclusive, trigger, actor_user_id, actor_key_id, actor_grant_id,
	phase, message, progress, error, result, ref_id, started_at, updated_at, finished_at`

func scanOperation(row interface{ Scan(...any) error }) (*Operation, error) {
	var op Operation
	var result string
	var started, updated int64
	var finished sql.NullInt64
	if err := row.Scan(&op.ID, &op.ServerID, &op.Kind, &op.Status, &op.Exclusive, &op.Trigger,
		&op.ActorUserID, &op.ActorKeyID, &op.ActorGrantID, &op.Phase, &op.Message, &op.Progress,
		&op.Error, &result, &op.RefID, &started, &updated, &finished); err != nil {
		return nil, err
	}
	if result != "" && result != "{}" {
		op.Result = json.RawMessage(result)
	}
	op.StartedAt = time.Unix(started, 0).UTC()
	op.UpdatedAt = time.Unix(updated, 0).UTC()
	if finished.Valid {
		t := time.Unix(finished.Int64, 0).UTC()
		op.FinishedAt = &t
	}
	return &op, nil
}

// CreateOperation records a new running operation. When op is exclusive and
// another exclusive operation is running on the server, it returns
// *ErrServerBusy naming that one.
func (s *Store) CreateOperation(ctx context.Context, op *Operation) error {
	if op.ID == "" {
		op.ID = uuid.NewString()
	}
	now := time.Now()
	op.Status = OpRunning
	op.StartedAt, op.UpdatedAt = now.UTC(), now.UTC()
	if op.Trigger == "" {
		op.Trigger = "manual"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operations (id, server_id, kind, status, exclusive, trigger, actor_user_id, actor_key_id,
		   actor_grant_id, phase, message, started_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		op.ID, op.ServerID, op.Kind, op.Status, op.Exclusive, op.Trigger, op.ActorUserID, op.ActorKeyID,
		op.ActorGrantID, op.Phase, op.Message, now.Unix(), now.Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") && op.Exclusive {
		current, _ := s.RunningOperation(ctx, op.ServerID)
		return &ErrServerBusy{Current: current}
	}
	return err
}

// UpdateOperationProgress records where a running operation has got to.
// progress is nil when there is no meaningful fraction.
func (s *Store) UpdateOperationProgress(ctx context.Context, id, phase, message string, progress *float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE operations SET phase = ?, message = ?, progress = ?, updated_at = ?
		  WHERE id = ? AND status = 'running'`,
		phase, message, progress, time.Now().Unix(), id)
	return err
}

// SetOperationRef links an operation to the record it produced (a backup, an
// update run, a migration).
func (s *Store) SetOperationRef(ctx context.Context, id, ref string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET ref_id = ? WHERE id = ?`, ref, id)
	return err
}

// FinishOperation closes a running operation. It is a no-op on one already
// finished, so a late second finish cannot rewrite the outcome.
func (s *Store) FinishOperation(ctx context.Context, id, status, errMsg string, result any) error {
	raw := []byte("{}")
	if result != nil {
		if b, err := json.Marshal(result); err == nil {
			raw = b
		}
	}
	if len(errMsg) > 2000 {
		errMsg = errMsg[:2000]
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE operations SET status = ?, error = ?, result = ?, updated_at = ?, finished_at = ?
		  WHERE id = ? AND status = 'running'`,
		status, errMsg, string(raw), now, now, id)
	return err
}

func (s *Store) GetOperation(ctx context.Context, id string) (*Operation, error) {
	op, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+opCols+` FROM operations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return op, err
}

// RunningOperation returns the exclusive operation running on a server, or nil.
func (s *Store) RunningOperation(ctx context.Context, serverID string) (*Operation, error) {
	op, err := scanOperation(s.db.QueryRowContext(ctx,
		`SELECT `+opCols+` FROM operations WHERE server_id = ? AND status = 'running' AND exclusive = 1`, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
}

// ListOperations returns a server's operations, newest first. before, when
// non-zero, is a unix-seconds cursor.
func (s *Store) ListOperations(ctx context.Context, serverID string, limit int, before int64) ([]*Operation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + opCols + ` FROM operations WHERE server_id = ?`
	args := []any{serverID}
	if before > 0 {
		q += ` AND started_at < ?`
		args = append(args, before)
	}
	q += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ListRunningOperations returns every running operation across the fleet.
func (s *Store) ListRunningOperations(ctx context.Context) ([]*Operation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+opCols+` FROM operations WHERE status = 'running' ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// InterruptRunningOperations marks every operation still recorded as running
// as interrupted. Run at startup: nothing can be running in a process that has
// only just begun, so these were cut off by the restart.
func (s *Store) InterruptRunningOperations(ctx context.Context) ([]*Operation, error) {
	running, err := s.ListRunningOperations(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for _, op := range running {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE operations SET status = 'interrupted', error = 'the panel restarted while this was in progress',
			   updated_at = ?, finished_at = ? WHERE id = ? AND status = 'running'`, now, now, op.ID); err != nil {
			return nil, err
		}
		op.Status = OpInterrupted
	}
	return running, nil
}

// ErrNotFound is returned by lookups that find nothing.
var ErrNotFound = errors.New("not found")

// InterruptRunningRecords closes records a previous process left running: a
// backup is marked failed (so it can be deleted), and safe-update and
// version-migration runs are marked interrupted. Nothing can be in flight in a
// process that has only just started.
func (s *Store) InterruptRunningRecords(ctx context.Context) (int, error) {
	total := 0
	for _, q := range []string{
		`UPDATE backups SET status = 'failed', completed_at = CURRENT_TIMESTAMP,
		        metadata = '{"error":"the panel restarted while this backup was running"}'
		  WHERE status = 'running'`,
		`UPDATE mod_update_runs SET status = 'interrupted', finished_at = CURRENT_TIMESTAMP WHERE status = 'running'`,
		`UPDATE server_version_migrations SET status = 'interrupted', finished_at = CURRENT_TIMESTAMP WHERE status = 'running'`,
	} {
		res, err := s.db.ExecContext(ctx, q)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}
