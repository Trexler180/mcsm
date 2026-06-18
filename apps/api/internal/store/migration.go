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

// VersionMigration is one server version-change attempt. Detail is a JSON
// progress document the engine rewrites as the run advances, so a "running" row
// can be polled for live progress; the final write records per-mod outcomes.
type VersionMigration struct {
	ID            string          `json:"id"`
	ServerID      string          `json:"server_id"`
	FromMCVersion string          `json:"from_mc_version"`
	ToMCVersion   string          `json:"to_mc_version"`
	BackupID      *string         `json:"backup_id"`
	Status        string          `json:"status"`
	Detail        json.RawMessage `json:"detail"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
}

func (s *Store) CreateVersionMigration(ctx context.Context, serverID, fromMC, toMC string, detail json.RawMessage) (*VersionMigration, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO server_version_migrations (id, server_id, from_mc_version, to_mc_version, status, detail)
		 VALUES (?,?,?,?,'running',?)`,
		id, serverID, fromMC, toMC, jsonRaw(detail),
	)
	if err != nil {
		return nil, err
	}
	return s.GetVersionMigration(ctx, id)
}

// UpdateVersionMigration rewrites a run's status + detail; finished also stamps
// finished_at (terminal states only). backupID, when non-nil, records the
// safety-net backup the engine took.
func (s *Store) UpdateVersionMigration(ctx context.Context, id, status string, detail json.RawMessage, backupID *string, finished bool) error {
	if finished {
		_, err := s.db.ExecContext(ctx,
			`UPDATE server_version_migrations SET status=?, detail=?, backup_id=COALESCE(?, backup_id), finished_at=CURRENT_TIMESTAMP WHERE id=?`,
			status, jsonRaw(detail), backupID, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE server_version_migrations SET status=?, detail=?, backup_id=COALESCE(?, backup_id) WHERE id=?`,
		status, jsonRaw(detail), backupID, id)
	return err
}

func (s *Store) GetVersionMigration(ctx context.Context, id string) (*VersionMigration, error) {
	var r VersionMigration
	var detail jsonRaw
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, from_mc_version, to_mc_version, backup_id, status, detail, started_at, finished_at
		 FROM server_version_migrations WHERE id = ?`, id,
	).Scan(&r.ID, &r.ServerID, &r.FromMCVersion, &r.ToMCVersion, &r.BackupID, &r.Status, &detail, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("version migration not found")
	}
	if err != nil {
		return nil, err
	}
	r.Detail = json.RawMessage(detail)
	return &r, nil
}

func (s *Store) ListVersionMigrations(ctx context.Context, serverID string, limit int) ([]*VersionMigration, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, from_mc_version, to_mc_version, backup_id, status, detail, started_at, finished_at
		 FROM server_version_migrations WHERE server_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?`,
		serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VersionMigration
	for rows.Next() {
		var r VersionMigration
		var detail jsonRaw
		if err := rows.Scan(&r.ID, &r.ServerID, &r.FromMCVersion, &r.ToMCVersion, &r.BackupID, &r.Status, &detail, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		r.Detail = json.RawMessage(detail)
		out = append(out, &r)
	}
	return out, rows.Err()
}
