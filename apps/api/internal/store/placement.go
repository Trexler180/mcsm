package store

import (
	"context"
	"errors"
	"strings"
)

// ServerPlacement is where a server sits in the dashboard: its folder (nil for
// ungrouped) and its position there.
type ServerPlacement struct {
	ServerID  string  `json:"id"`
	FolderID  *string `json:"folder_id"`
	SortOrder int     `json:"sort_order"`
}

// ErrUnknownServerOrFolder rejects a placement naming a server or folder that
// does not exist.
var ErrUnknownServerOrFolder = errors.New("unknown server or folder")

// PlaceServers moves servers into folders and positions in one transaction,
// returning where they were before so the move can be undone by placing them
// back.
func (s *Store) PlaceServers(ctx context.Context, entries []ServerPlacement) ([]ServerPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	previous := make([]ServerPlacement, 0, len(entries))
	for _, e := range entries {
		var p ServerPlacement
		p.ServerID = e.ServerID
		if err := tx.QueryRowContext(ctx, `SELECT folder_id, sort_order FROM servers WHERE id = ?`, e.ServerID).
			Scan(&p.FolderID, &p.SortOrder); err != nil {
			return nil, ErrUnknownServerOrFolder
		}
		previous = append(previous, p)
		if _, err := tx.ExecContext(ctx,
			`UPDATE servers SET folder_id = ?, sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			e.FolderID, e.SortOrder, e.ServerID); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return nil, ErrUnknownServerOrFolder
			}
			return nil, err
		}
	}
	return previous, tx.Commit()
}

// FolderPlacements lists the placement of every server in a folder, for
// restoring it after the folder is deleted.
func (s *Store) FolderPlacements(ctx context.Context, folderID string) ([]ServerPlacement, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, folder_id, sort_order FROM servers WHERE folder_id = ? ORDER BY sort_order, lower(name)`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerPlacement{}
	for rows.Next() {
		var p ServerPlacement
		if err := rows.Scan(&p.ServerID, &p.FolderID, &p.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
