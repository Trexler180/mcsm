package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ── Servers ──────────────────────────────────────────────────────

func (s *Store) CreateServer(ctx context.Context, srv *Server) (*Server, error) {
	id := uuid.NewString()
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, auto_start, tags, settings)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, srv.NodeID, srv.OwnerID, srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings),
	)
	if err != nil {
		return nil, err
	}
	return s.GetServer(ctx, id)
}

func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	var srv Server
	err := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers WHERE id = ?`, id,
	).Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
		&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
		&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server not found")
	}
	return &srv, err
}

func (s *Store) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, &srv)
	}
	return servers, rows.Err()
}

func (s *Store) CountServersForNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE node_id = ?`, nodeID).Scan(&n)
	return n, err
}

func (s *Store) ListServersForUser(ctx context.Context, userID string) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers
		 WHERE owner_id = ?
		    OR id IN (
		        SELECT sp.server_id FROM server_permissions sp
		        WHERE sp.user_id = ? AND json_array_length(sp.permissions) > 0
		    )
		 ORDER BY name`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, &srv)
	}
	return servers, rows.Err()
}

func (s *Store) UserCanAccessServer(ctx context.Context, userID, serverID string) (bool, error) {
	return s.UserHasServerPermission(ctx, userID, serverID, ServerPermissionView)
}

func (s *Store) UserHasServerPermission(ctx context.Context, userID, serverID string, needed ServerPermission) (bool, error) {
	var ownerID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM servers WHERE id = ?`, serverID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ownerID == userID {
		return true, nil
	}
	perms, ok, err := s.GetServerPermissions(ctx, serverID, userID)
	if err != nil || !ok {
		return false, err
	}
	return HasServerPermission(perms, needed), nil
}

// UserHasServerGroupAccess reports whether a user can reach a group's
// read-level endpoints: true for the owner, and for any collaborator holding
// the group or any of its leaves.
func (s *Store) UserHasServerGroupAccess(ctx context.Context, userID, serverID string, group ServerPermission) (bool, error) {
	var ownerID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM servers WHERE id = ?`, serverID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ownerID == userID {
		return true, nil
	}
	perms, ok, err := s.GetServerPermissions(ctx, serverID, userID)
	if err != nil || !ok {
		return false, err
	}
	return HasServerGroupAccess(perms, group), nil
}

func (s *Store) ListServerMembers(ctx context.Context, serverID string) ([]*ServerMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sp.server_id, sp.user_id, u.email, u.display_name, u.role, sp.permissions
		 FROM server_permissions sp
		 JOIN users u ON u.id = sp.user_id
		 WHERE sp.server_id = ?
		 ORDER BY lower(u.email)`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*ServerMember
	for rows.Next() {
		var m ServerMember
		var perms strArray
		if err := rows.Scan(&m.ServerID, &m.UserID, &m.Email, &m.DisplayName, &m.Role, &perms); err != nil {
			return nil, err
		}
		normalized, err := NormalizeServerPermissions([]string(perms))
		if err != nil {
			return nil, err
		}
		m.Permissions = normalized
		members = append(members, &m)
	}
	return members, rows.Err()
}

func (s *Store) GetServerMember(ctx context.Context, serverID, userID string) (*ServerMember, error) {
	var m ServerMember
	var perms strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT sp.server_id, sp.user_id, u.email, u.display_name, u.role, sp.permissions
		 FROM server_permissions sp
		 JOIN users u ON u.id = sp.user_id
		 WHERE sp.server_id = ? AND sp.user_id = ?`,
		serverID, userID,
	).Scan(&m.ServerID, &m.UserID, &m.Email, &m.DisplayName, &m.Role, &perms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeServerPermissions([]string(perms))
	if err != nil {
		return nil, err
	}
	m.Permissions = normalized
	return &m, nil
}

func (s *Store) SetServerPermissions(ctx context.Context, serverID, userID string, perms []string) error {
	normalized, err := NormalizeServerPermissions(perms)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO server_permissions (server_id, user_id, permissions)
		 VALUES (?, ?, ?)
		 ON CONFLICT(server_id, user_id) DO UPDATE SET permissions=excluded.permissions`,
		serverID, userID, strArray(normalized),
	)
	return err
}

func (s *Store) SetServerPermissionsIfCurrent(ctx context.Context, serverID, userID string, perms, expected []string) error {
	normalized, err := NormalizeServerPermissions(perms)
	if err != nil {
		return err
	}
	expectedNormalized, err := NormalizeServerPermissions(expected)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current strArray
	err = tx.QueryRowContext(ctx,
		`SELECT permissions FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerMemberNotFound
	}
	if err != nil {
		return err
	}
	currentNormalized, err := NormalizeServerPermissions([]string(current))
	if err != nil {
		return err
	}
	if !samePermissionSet(currentNormalized, expectedNormalized) {
		return ErrServerPermissionsStale
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE server_permissions SET permissions = ? WHERE server_id = ? AND user_id = ?`,
		strArray(normalized), serverID, userID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteServerPermissions(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	)
	return err
}

func (s *Store) GetServerPermissions(ctx context.Context, serverID, userID string) ([]string, bool, error) {
	var perms strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT permissions FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	).Scan(&perms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	normalized, err := NormalizeServerPermissions([]string(perms))
	if err != nil {
		return nil, true, err
	}
	return normalized, true, nil
}

func (s *Store) UpdateServer(ctx context.Context, id string, srv *Server) error {
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name=?, description=?, platform=?, mc_version=?, loader_version=?,
		  directory_path=?, java_binary=?, jvm_args=?, port=?, ram_mb_min=?, ram_mb_max=?,
		  auto_start=?, tags=?, settings=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings), id,
	)
	return err
}

func (s *Store) UpdateServerStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id)
	return err
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	return err
}
