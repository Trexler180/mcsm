package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SkippedModVersion is a version the auto-updater must not install on a server:
// installing it broke the server boot, the update was reverted, and the version
// was blocklisted so later runs pick the next one instead.
type SkippedModVersion struct {
	ServerID  string    `json:"server_id"`
	ProjectID string    `json:"project_id"`
	VersionID string    `json:"version_id"`
	ModName   string    `json:"mod_name"`
	Version   string    `json:"version"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// ModUpdateRun is one auto-update attempt. Detail is a JSON progress document
// the engine rewrites as the run advances, so a "running" row can be polled for
// live progress; the final write records per-mod outcomes.
type ModUpdateRun struct {
	ID         string          `json:"id"`
	ServerID   string          `json:"server_id"`
	Trigger    string          `json:"trigger"`
	Status     string          `json:"status"`
	Detail     json.RawMessage `json:"detail"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
}

// ── Skipped versions ─────────────────────────────────────────────

// AddSkippedModVersion blocklists a version. Idempotent; the latest reason wins.
func (s *Store) AddSkippedModVersion(ctx context.Context, v *SkippedModVersion) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mod_skipped_versions (server_id, project_id, version_id, mod_name, version, reason)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(server_id, project_id, version_id) DO UPDATE SET
		   mod_name=excluded.mod_name, version=excluded.version, reason=excluded.reason`,
		v.ServerID, v.ProjectID, v.VersionID, v.ModName, v.Version, v.Reason,
	)
	return err
}

func (s *Store) ListSkippedModVersions(ctx context.Context, serverID string) ([]*SkippedModVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT server_id, project_id, version_id, mod_name, version, reason, created_at
		 FROM mod_skipped_versions WHERE server_id = ? ORDER BY created_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SkippedModVersion
	for rows.Next() {
		var v SkippedModVersion
		if err := rows.Scan(&v.ServerID, &v.ProjectID, &v.VersionID, &v.ModName, &v.Version, &v.Reason, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// DeleteSkippedModVersion un-blocklists a version so the updater may try it again.
func (s *Store) DeleteSkippedModVersion(ctx context.Context, serverID, projectID, versionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM mod_skipped_versions WHERE server_id = ? AND project_id = ? AND version_id = ?`,
		serverID, projectID, versionID,
	)
	return err
}

// ── Update runs ──────────────────────────────────────────────────

func (s *Store) CreateModUpdateRun(ctx context.Context, serverID, trigger string, detail json.RawMessage) (*ModUpdateRun, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mod_update_runs (id, server_id, trigger, status, detail) VALUES (?,?,?,'running',?)`,
		id, serverID, trigger, jsonRaw(detail),
	)
	if err != nil {
		return nil, err
	}
	return s.GetModUpdateRun(ctx, id)
}

// UpdateModUpdateRun rewrites a run's status + detail; finished also stamps
// finished_at (terminal states only).
func (s *Store) UpdateModUpdateRun(ctx context.Context, id, status string, detail json.RawMessage, finished bool) error {
	if finished {
		_, err := s.db.ExecContext(ctx,
			`UPDATE mod_update_runs SET status=?, detail=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`,
			status, jsonRaw(detail), id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE mod_update_runs SET status=?, detail=? WHERE id=?`, status, jsonRaw(detail), id)
	return err
}

func (s *Store) GetModUpdateRun(ctx context.Context, id string) (*ModUpdateRun, error) {
	var r ModUpdateRun
	var detail jsonRaw
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, trigger, status, detail, started_at, finished_at
		 FROM mod_update_runs WHERE id = ?`, id,
	).Scan(&r.ID, &r.ServerID, &r.Trigger, &r.Status, &detail, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("update run not found")
	}
	if err != nil {
		return nil, err
	}
	r.Detail = json.RawMessage(detail)
	return &r, nil
}

func (s *Store) ListModUpdateRuns(ctx context.Context, serverID string, limit int) ([]*ModUpdateRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, trigger, status, detail, started_at, finished_at
		 FROM mod_update_runs WHERE server_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?`,
		serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ModUpdateRun
	for rows.Next() {
		var r ModUpdateRun
		var detail jsonRaw
		if err := rows.Scan(&r.ID, &r.ServerID, &r.Trigger, &r.Status, &detail, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		r.Detail = json.RawMessage(detail)
		out = append(out, &r)
	}
	return out, rows.Err()
}
