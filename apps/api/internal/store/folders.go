package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ── Server folders ───────────────────────────────────────────────
//
// A flat grouping over servers. Folders hold no permissions themselves — the
// listing helpers scope a folder's visibility and its server count to the
// servers the caller can already reach, so a folder full of servers you can't
// see simply doesn't exist as far as you're concerned.

// folderOrder keeps every folder listing in the same sequence the UI renders:
// explicit sort_order first, then name, so ties are stable rather than
// insertion-ordered.
const folderOrder = ` ORDER BY f.sort_order, lower(f.name)`

// ListServerFolders returns every folder with a fleet-wide server count. Admin
// view: nothing is filtered out, including empty folders.
func (s *Store) ListServerFolders(ctx context.Context) ([]*ServerFolder, error) {
	return s.queryFolders(ctx,
		`SELECT f.id, f.name, f.description, f.color, f.sort_order, f.created_at, f.updated_at,
		  (SELECT COUNT(*) FROM servers sv WHERE sv.folder_id = f.id) AS server_count
		 FROM server_folders f`+folderOrder)
}

// ListServerFoldersForUser returns only the folders holding at least one server
// the user can access, counting just those servers. A folder the user has no
// business seeing never reaches them, and a count never leaks the existence of
// servers they can't open.
func (s *Store) ListServerFoldersForUser(ctx context.Context, userID string) ([]*ServerFolder, error) {
	// Mirrors ListServersForUser's visibility rule: owned, or shared with at
	// least one permission. Used as a correlated predicate on f.id, so it has to
	// sit in scalar/EXISTS subqueries rather than a derived table.
	const visible = `
		sv.folder_id = f.id
		  AND (sv.owner_id = ?
		       OR sv.id IN (
		           SELECT sp.server_id FROM server_permissions sp
		           WHERE sp.user_id = ? AND json_array_length(sp.permissions) > 0
		       ))`
	return s.queryFolders(ctx,
		`SELECT f.id, f.name, f.description, f.color, f.sort_order, f.created_at, f.updated_at,
		  (SELECT COUNT(*) FROM servers sv WHERE`+visible+`) AS server_count
		 FROM server_folders f
		 WHERE EXISTS (SELECT 1 FROM servers sv WHERE`+visible+`)`+folderOrder,
		userID, userID, userID, userID)
}

func (s *Store) queryFolders(ctx context.Context, query string, args ...any) ([]*ServerFolder, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := []*ServerFolder{}
	for rows.Next() {
		var f ServerFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.Color, &f.SortOrder,
			&f.CreatedAt, &f.UpdatedAt, &f.ServerCount); err != nil {
			return nil, err
		}
		folders = append(folders, &f)
	}
	return folders, rows.Err()
}

func (s *Store) GetServerFolder(ctx context.Context, id string) (*ServerFolder, error) {
	var f ServerFolder
	err := s.db.QueryRowContext(ctx,
		`SELECT f.id, f.name, f.description, f.color, f.sort_order, f.created_at, f.updated_at,
		  (SELECT COUNT(*) FROM servers sv WHERE sv.folder_id = f.id) AS server_count
		 FROM server_folders f WHERE f.id = ?`, id,
	).Scan(&f.ID, &f.Name, &f.Description, &f.Color, &f.SortOrder,
		&f.CreatedAt, &f.UpdatedAt, &f.ServerCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) CreateServerFolder(ctx context.Context, f *ServerFolder) (*ServerFolder, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO server_folders (id, name, description, color, sort_order)
		 VALUES (?,?,?,?,?)`,
		id, f.Name, f.Description, f.Color, f.SortOrder,
	)
	if err != nil {
		if isFolderNameConflict(err) {
			return nil, ErrFolderNameTaken
		}
		return nil, err
	}
	return s.GetServerFolder(ctx, id)
}

func (s *Store) UpdateServerFolder(ctx context.Context, id string, f *ServerFolder) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE server_folders SET name=?, description=?, color=?, sort_order=?,
		  updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		f.Name, f.Description, f.Color, f.SortOrder, id,
	)
	if err != nil {
		if isFolderNameConflict(err) {
			return ErrFolderNameTaken
		}
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrFolderNotFound
	}
	return nil
}

// DeleteServerFolder removes the folder only; its servers survive and fall back
// to ungrouped via the FK's ON DELETE SET NULL.
func (s *Store) DeleteServerFolder(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM server_folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrFolderNotFound
	}
	return nil
}

// SetServerFolder moves one server into a folder, or out of every folder when
// folderID is nil.
func (s *Store) SetServerFolder(ctx context.Context, serverID string, folderID *string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET folder_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		folderID, serverID)
	return err
}

// isFolderNameConflict distinguishes the case-insensitive name index from any
// other constraint failure, so a duplicate name reports as 409 rather than 500.
func isFolderNameConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") &&
		strings.Contains(msg, "server_folders")
}
