package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ── Backup Targets ───────────────────────────────────────────────

func (s *Store) ListBackupTargets(ctx context.Context, serverID string) ([]*BackupTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, type, config, retention, is_default FROM backup_targets WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []*BackupTarget
	for rows.Next() {
		var t BackupTarget
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Type, (*jsonRaw)(&t.Config), (*jsonRaw)(&t.Retention), &t.IsDefault); err != nil {
			return nil, err
		}
		targets = append(targets, &t)
	}
	return targets, rows.Err()
}

func (s *Store) CreateBackupTarget(ctx context.Context, t *BackupTarget) (*BackupTarget, error) {
	id := uuid.NewString()
	if t.Config == nil {
		t.Config = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_targets (id, server_id, name, type, config, retention, is_default)
		 VALUES (?,?,?,?,?,?,?)`,
		id, t.ServerID, t.Name, t.Type, jsonRaw(t.Config), jsonRaw(t.Retention), t.IsDefault,
	)
	if err != nil {
		return nil, err
	}
	var out BackupTarget
	err = s.db.QueryRowContext(ctx,
		`SELECT id, server_id, name, type, config, retention, is_default FROM backup_targets WHERE id = ?`, id,
	).Scan(&out.ID, &out.ServerID, &out.Name, &out.Type, (*jsonRaw)(&out.Config), (*jsonRaw)(&out.Retention), &out.IsDefault)
	return &out, err
}

// ── Backups ──────────────────────────────────────────────────────

func (s *Store) ListBackups(ctx context.Context, serverID string) ([]*Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE server_id=? ORDER BY started_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var backups []*Backup
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.ServerID, &b.TargetID, &b.TriggeredBy, &b.Trigger, &b.Status, &b.SizeBytes, &b.SnapshotID, (*jsonRaw)(&b.Metadata), &b.StartedAt, &b.CompletedAt); err != nil {
			return nil, err
		}
		backups = append(backups, &b)
	}
	return backups, rows.Err()
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, id)
	return err
}

func (s *Store) GetBackup(ctx context.Context, id string) (*Backup, error) {
	var b Backup
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE id=?`, id,
	).Scan(&b.ID, &b.ServerID, &b.TargetID, &b.TriggeredBy, &b.Trigger, &b.Status, &b.SizeBytes, &b.SnapshotID, (*jsonRaw)(&b.Metadata), &b.StartedAt, &b.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("backup not found")
	}
	return &b, err
}

// UpdateBackupResult marks a running backup as success/failed. Pass nil
// sizeBytes to leave the column unchanged (e.g. on failure).
func (s *Store) UpdateBackupResult(ctx context.Context, id, status string, sizeBytes *int64, errMsg string) error {
	if sizeBytes != nil {
		_, err := s.db.ExecContext(ctx,
			`UPDATE backups SET status=?, size_bytes=?, completed_at=CURRENT_TIMESTAMP WHERE id=?`,
			status, *sizeBytes, id)
		return err
	}
	// On failure, store the error message in metadata for visibility
	meta, _ := json.Marshal(map[string]string{"error": errMsg})
	_, err := s.db.ExecContext(ctx,
		`UPDATE backups SET status=?, completed_at=CURRENT_TIMESTAMP, metadata=? WHERE id=?`,
		status, string(meta), id)
	return err
}

func (s *Store) CreateBackup(ctx context.Context, b *Backup) (*Backup, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backups (id, server_id, target_id, triggered_by, trigger, status)
		 VALUES (?,?,?,?,?,?)`,
		id, b.ServerID, b.TargetID, b.TriggeredBy, b.Trigger, b.Status,
	)
	if err != nil {
		return nil, err
	}
	var out Backup
	err = s.db.QueryRowContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE id = ?`, id,
	).Scan(&out.ID, &out.ServerID, &out.TargetID, &out.TriggeredBy, &out.Trigger, &out.Status, &out.SizeBytes, &out.SnapshotID, (*jsonRaw)(&out.Metadata), &out.StartedAt, &out.CompletedAt)
	return &out, err
}
