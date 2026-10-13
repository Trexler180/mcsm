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

// ── Servers ──────────────────────────────────────────────────────

func (s *Store) CreateServer(ctx context.Context, srv *Server) (*Server, error) {
	id := uuid.NewString()
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, auto_start, tags, settings, folder_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, srv.NodeID, srv.OwnerID, srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings), srv.FolderID,
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
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings,
		  folder_id, sort_order, public_status, COALESCE(public_slug, ''), created_at, updated_at,
		  (SELECT MAX(u.started_at) FROM server_uptime u WHERE u.server_id = servers.id AND u.ended_at IS NULL) AS online_since
		 FROM servers WHERE id = ?`, id,
	).Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
		&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
		&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings),
		&srv.FolderID, &srv.SortOrder, &srv.PublicStatus, &srv.PublicSlug, &srv.CreatedAt, &srv.UpdatedAt, &srv.OnlineSince)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server %w", ErrNotFound)
	}
	return &srv, err
}

// GetServerByPublicSlug resolves a public status page slug to its server —
// only when the page is enabled. Callers treat any error as "not found" so a
// disabled page is indistinguishable from a nonexistent one.
func (s *Store) GetServerByPublicSlug(ctx context.Context, slug string) (*Server, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE public_status = 1 AND public_slug = ? AND public_slug != ''`,
		slug,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server %w", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return s.GetServer(ctx, id)
}

func (s *Store) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings,
		  folder_id, sort_order, public_status, COALESCE(public_slug, ''), created_at, updated_at,
		  (SELECT MAX(u.started_at) FROM server_uptime u WHERE u.server_id = servers.id AND u.ended_at IS NULL) AS online_since
		 FROM servers ORDER BY sort_order, lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings),
			&srv.FolderID, &srv.SortOrder, &srv.PublicStatus, &srv.PublicSlug, &srv.CreatedAt, &srv.UpdatedAt, &srv.OnlineSince); err != nil {
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
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings,
		  folder_id, sort_order, public_status, COALESCE(public_slug, ''), created_at, updated_at,
		  (SELECT MAX(u.started_at) FROM server_uptime u WHERE u.server_id = servers.id AND u.ended_at IS NULL) AS online_since
		 FROM servers
		 WHERE owner_id = ?
		    OR id IN (
		        SELECT sp.server_id FROM server_permissions sp
		        WHERE sp.user_id = ? AND json_array_length(sp.permissions) > 0
		    )
		 ORDER BY sort_order, lower(name)`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings),
			&srv.FolderID, &srv.SortOrder, &srv.PublicStatus, &srv.PublicSlug, &srv.CreatedAt, &srv.UpdatedAt, &srv.OnlineSince); err != nil {
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

// AllServerPermissionsForUser returns every server the user has an explicit
// grant on, keyed by server id. Listing servers needs the caller's permissions
// on each one so the panel can gate per-server actions; doing that with one
// query beats a GetServerPermissions call per row.
//
// Owned servers are not included — the caller resolves those to the full set,
// since ownership outranks any stored grant.
func (s *Store) AllServerPermissionsForUser(ctx context.Context, userID string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT server_id, permissions FROM server_permissions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var serverID string
		var perms strArray
		if err := rows.Scan(&serverID, &perms); err != nil {
			return nil, err
		}
		normalized, err := NormalizeServerPermissions([]string(perms))
		if err != nil {
			return nil, err
		}
		out[serverID] = normalized
	}
	return out, rows.Err()
}

func (s *Store) UpdateServer(ctx context.Context, id string, srv *Server) error {
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name=?, description=?, platform=?, mc_version=?, loader_version=?,
		  directory_path=?, java_binary=?, jvm_args=?, port=?, ram_mb_min=?, ram_mb_max=?,
		  auto_start=?, tags=?, settings=?, folder_id=?, public_status=?, public_slug=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings), srv.FolderID, srv.PublicStatus, srv.PublicSlug, id,
	)
	return err
}

// UpdateServerStatus persists a server's status and maintains its uptime
// segments on the online boundary: entering "online" opens a segment, leaving
// it closes the open one as a clean stop. The poller uses
// UpdateServerStatusCrash for offline transitions it attributes to a crash.
func (s *Store) UpdateServerStatus(ctx context.Context, id, status string) error {
	return s.updateServerStatus(ctx, id, status, "stop")
}

// UpdateServerStatusCrash is UpdateServerStatus for a transition the caller
// knows was not panel-initiated; the closed uptime segment records "crash".
func (s *Store) UpdateServerStatusCrash(ctx context.Context, id, status string) error {
	return s.updateServerStatus(ctx, id, status, "crash")
}

func (s *Store) updateServerStatus(ctx context.Context, id, status, endReason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var prev string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM servers WHERE id = ?`, id).Scan(&prev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Matches the historical UPDATE-only behavior: a status write for a
			// deleted server is a silent no-op, not an error.
			return nil
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id); err != nil {
		return err
	}

	now := time.Now().Unix()
	switch {
	case prev != "online" && status == "online":
		// Close any dangling open segment first (shouldn't exist, but a stray
		// one would otherwise double-count from here on), then open a new one.
		if _, err := tx.ExecContext(ctx,
			`UPDATE server_uptime SET ended_at=? WHERE server_id=? AND ended_at IS NULL`, now, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO server_uptime (server_id, started_at) VALUES (?,?)`, id, now); err != nil {
			return err
		}
	case prev == "online" && status != "online":
		if _, err := tx.ExecContext(ctx,
			`UPDATE server_uptime SET ended_at=?, end_reason=? WHERE server_id=? AND ended_at IS NULL`,
			now, endReason, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	return err
}

// UpdateServerStatusUnreachable records that a server's node stopped answering.
// The status becomes "unknown" and an open uptime segment is closed as
// "unreachable" rather than as a stop or crash: the panel cannot see the
// server, which is not the same as the server being down.
func (s *Store) UpdateServerStatusUnreachable(ctx context.Context, id string) error {
	return s.updateServerStatus(ctx, id, "unknown", "unreachable")
}

// ResumeUptime handles a node coming back after an outage. When the server's
// process started before the outage ended, it ran straight through it, so the
// uptime segment closed as "unreachable" is reopened as if the outage never
// interrupted it, and the status is set to online. It reports whether it did;
// when it did not, the caller records a fresh transition.
func (s *Store) ResumeUptime(ctx context.Context, id string, processStartedAt time.Time) (bool, error) {
	if processStartedAt.IsZero() {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var segID, endedAt int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, ended_at FROM server_uptime
		  WHERE server_id = ? AND end_reason = 'unreachable' AND ended_at IS NOT NULL
		  ORDER BY started_at DESC LIMIT 1`, id).Scan(&segID, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if processStartedAt.Unix() > endedAt {
		return false, nil // a new process: the old one really stopped
	}
	var open int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM server_uptime WHERE server_id = ? AND ended_at IS NULL`, id).Scan(&open); err != nil {
		return false, err
	}
	if open > 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE server_uptime SET ended_at = NULL, end_reason = '' WHERE id = ?`, segID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
