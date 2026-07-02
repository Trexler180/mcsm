package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ── Installed Mods ───────────────────────────────────────────────

const modCols = `id, server_id, source, source_id, version_id, name, version, file_name, sha256, sha512, pinned, enabled, install_path, installed_as_dep, installed_at`

func scanMod(sc interface {
	Scan(...any) error
}, m *InstalledMod) error {
	return sc.Scan(&m.ID, &m.ServerID, &m.Source, &m.SourceID, &m.VersionID, &m.Name, &m.Version,
		&m.FileName, &m.SHA256, &m.SHA512, &m.Pinned, &m.Enabled, &m.InstallPath, &m.InstalledAsDep, &m.InstalledAt)
}

func (s *Store) ListMods(ctx context.Context, serverID string) ([]*InstalledMod, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+modCols+` FROM installed_mods WHERE server_id = ? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mods []*InstalledMod
	for rows.Next() {
		var m InstalledMod
		if err := scanMod(rows, &m); err != nil {
			return nil, err
		}
		mods = append(mods, &m)
	}
	return mods, rows.Err()
}

func (s *Store) CreateMod(ctx context.Context, m *InstalledMod) (*InstalledMod, error) {
	id := uuid.NewString()
	if m.InstallPath == "" {
		m.InstallPath = "/mods"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO installed_mods (id, server_id, source, source_id, version_id, name, version, file_name, sha256, sha512, pinned, install_path, installed_as_dep)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, m.ServerID, m.Source, m.SourceID, m.VersionID, m.Name, m.Version, m.FileName, m.SHA256, m.SHA512, m.Pinned, m.InstallPath, m.InstalledAsDep,
	)
	if err != nil {
		return nil, err
	}
	var out InstalledMod
	err = scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, id), &out)
	return &out, err
}

// StampModHash records the file's sha512 without otherwise changing the row. A
// stored hash marks the jar as "already hashed and looked up", so reconciliation
// won't rehash or re-query it on every Mods-tab load.
func (s *Store) StampModHash(ctx context.Context, id, sha512 string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET sha512=? WHERE id=?`, sha512, id)
	return err
}

// RecognizeMod upgrades an existing (typically "custom") row to a known source
// after its jar was matched to that source's file index by hash — promoting it to
// a managed mod with version metadata and update checking, while keeping the same
// on-disk file and enabled state.
func (s *Store) RecognizeMod(ctx context.Context, id, source, sourceID, versionID, name, version, sha512 string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE installed_mods SET source=?, source_id=?, version_id=?, name=?, version=?, sha512=? WHERE id=?`,
		source, sourceID, versionID, name, version, sha512, id,
	)
	return err
}

func (s *Store) GetMod(ctx context.Context, id string) (*InstalledMod, error) {
	var m InstalledMod
	err := scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, id), &m)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("mod not found")
	}
	return &m, err
}

// UpdateMod swaps the version/file metadata of an existing mod row (used by the
// update flow after a new jar is pushed to the agent).
func (s *Store) UpdateMod(ctx context.Context, m *InstalledMod) (*InstalledMod, error) {
	_, err := s.db.ExecContext(ctx,
		`UPDATE installed_mods SET version_id=?, name=?, version=?, file_name=?, sha256=? WHERE id=?`,
		m.VersionID, m.Name, m.Version, m.FileName, m.SHA256, m.ID,
	)
	if err != nil {
		return nil, err
	}
	var out InstalledMod
	err = scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, m.ID), &out)
	return &out, err
}

// SetModPinned toggles whether a mod is excluded from bulk updates.
func (s *Store) SetModPinned(ctx context.Context, id string, pinned bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET pinned=? WHERE id=?`, pinned, id)
	return err
}

// SetModEnabled records the enabled state and the resulting on-disk file name
// (jars are renamed to "<name>.disabled" when disabled, so file_name must follow
// so uninstall/update keep targeting the real file).
func (s *Store) SetModEnabled(ctx context.Context, id string, enabled bool, fileName string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET enabled=?, file_name=? WHERE id=?`, enabled, fileName, id)
	return err
}

func (s *Store) DeleteMod(ctx context.Context, id string) (*InstalledMod, error) {
	var m InstalledMod
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, file_name FROM installed_mods WHERE id = ?`, id,
	).Scan(&m.ID, &m.ServerID, &m.FileName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("mod not found")
	}
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM installed_mods WHERE id = ?`, id)
	return &m, err
}

// ── Mod dependency graph ─────────────────────────────────────────

// AddModDependency records that dependentPID requires dependencyPID on a server.
// Idempotent: re-recording the same edge is a no-op.
func (s *Store) AddModDependency(ctx context.Context, serverID, dependentPID, dependencyPID string) error {
	if dependentPID == "" || dependencyPID == "" || dependentPID == dependencyPID {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO mod_dependencies (server_id, dependent_project_id, dependency_project_id)
		 VALUES (?,?,?)`,
		serverID, dependentPID, dependencyPID,
	)
	return err
}

// ListModDependencies returns every dependency edge for a server.
func (s *Store) ListModDependencies(ctx context.Context, serverID string) ([]ModDependency, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dependent_project_id, dependency_project_id FROM mod_dependencies WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []ModDependency
	for rows.Next() {
		var e ModDependency
		if err := rows.Scan(&e.DependentProjectID, &e.DependencyProjectID); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// DeleteModDependencyEdges removes every edge touching a project id (as either
// side) on a server — used on uninstall so the removed mod stops counting as a
// dependent and any now-unreferenced deps become orphaned.
func (s *Store) DeleteModDependencyEdges(ctx context.Context, serverID, projectID string) error {
	if projectID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM mod_dependencies
		 WHERE server_id = ? AND (dependent_project_id = ? OR dependency_project_id = ?)`,
		serverID, projectID, projectID,
	)
	return err
}
