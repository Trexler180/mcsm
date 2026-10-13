package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
)

// ── Audit Log ────────────────────────────────────────────────────

type AuditEntry struct {
	ID       int64   `json:"id"`
	UserID   *string `json:"user_id"`
	ServerID *string `json:"server_id"`
	// ServerName is the server's name when the row was written, so the history
	// of a deleted server stays readable.
	ServerName *string `json:"server_name,omitempty"`
	// APIKeyID names the agent access key that acted, when one did. Nil for an
	// interactive human action and for background/system work, so every
	// existing writer and reader stays valid.
	APIKeyID *string `json:"api_key_id"`
	// MCPGrantID names the remote-agent delegation that acted, when one did.
	// Nil for human sessions, access keys, and background work — so every
	// existing writer and reader stays valid.
	MCPGrantID *string   `json:"mcp_grant_id"`
	Action     string    `json:"action"`
	Detail     *string   `json:"detail"`
	IPAddress  *string   `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
}

// LogAction records a human or system action. Machine actions go through
// LogActionWithKey or LogActionWithActor so the responsible credential is named
// too.
func (s *Store) LogAction(ctx context.Context, userID, serverID, action, ip string, detail any) error {
	return s.LogActionWithActor(ctx, userID, "", "", serverID, action, ip, detail)
}

// LogActionWithKey records an action attributed to a human owner and one of
// their access keys.
func (s *Store) LogActionWithKey(ctx context.Context, userID, apiKeyID, serverID, action, ip string, detail any) error {
	return s.LogActionWithActor(ctx, userID, apiKeyID, "", serverID, action, ip, detail)
}

// LogActionWithActor records an action attributed to the human owner and, when
// the request came from automation, to the exact credential behind it: an
// access key, or a remote-agent MCP grant. Keeping user_id populated preserves
// the accountability chain — revoking a key or a grant never erases who was
// responsible for it.
//
// At most one of apiKeyID and mcpGrantID is ever set: a request arrives on one
// credential family or the other, never both.
func (s *Store) LogActionWithActor(ctx context.Context, userID, apiKeyID, mcpGrantID, serverID, action, ip string, detail any) error {
	d, err := json.Marshal(detail)
	if err != nil {
		slog.Error("audit detail serialization failed", "action", action, "server_id", serverID, "user_id", userID, "error", err)
		return err
	}
	var uid, sid, kid, gid *string
	if userID != "" {
		uid = &userID
	}
	if serverID != "" {
		sid = &serverID
	}
	if apiKeyID != "" {
		kid = &apiKeyID
	}
	if mcpGrantID != "" {
		gid = &mcpGrantID
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, api_key_id, mcp_grant_id, server_id, server_name, action, detail, ip_address)
		 VALUES (?,?,?,?,(SELECT name FROM servers WHERE id = ?),?,?,?)`,
		uid, kid, gid, sid, sid, action, string(d), ip,
	)
	if err != nil {
		slog.Error("audit log write failed", "action", action, "server_id", serverID, "user_id", userID, "error", err)
	}
	return err
}

// ListAudit returns the most recent audit entries, optionally scoped to one
// server. limit defaults to 100, capped at 500.
func (s *Store) ListAudit(ctx context.Context, serverID string, limit int) ([]*AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT id, user_id, api_key_id, mcp_grant_id, server_id, server_name, action, detail, ip_address, created_at FROM audit_log`
	args := []any{}
	if serverID != "" {
		q += ` WHERE server_id = ?`
		args = append(args, serverID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.APIKeyID, &e.MCPGrantID, &e.ServerID, &e.ServerName, &e.Action, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// AuditQuery selects audit rows. The zero value returns the newest rows across
// everything, which is the admin view; Scope narrows it for everyone else.
type AuditQuery struct {
	// Scope, when non-nil, limits rows to these servers plus the caller's own
	// panel-wide actions (logins, keys, sessions). Global rows naming anyone
	// else — another user's login IP, a node reboot, an integration change —
	// are the admin-only log and never pass a scope.
	Scope *AuditScope

	// Optional filters.
	ServerID     string
	UserID       string
	ActionPrefix string
	Since        time.Time
	Until        time.Time

	// Before is the cursor: only rows with an id below it. Zero means start at
	// the newest.
	Before int64
	Limit  int
}

// AuditScope is a non-admin caller's visibility.
type AuditScope struct {
	ServerIDs []string
	UserID    string
}

// ListAuditPage returns one page of audit rows, newest first, and the cursor
// for the next page (0 when there is none).
func (s *Store) ListAuditPage(ctx context.Context, q AuditQuery) ([]*AuditEntry, int64, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	var conds []string
	var args []any
	if q.Scope != nil {
		parts := []string{}
		if len(q.Scope.ServerIDs) > 0 {
			parts = append(parts, `server_id IN (`+placeholders(len(q.Scope.ServerIDs))+`)`)
			for _, id := range q.Scope.ServerIDs {
				args = append(args, id)
			}
		}
		if q.Scope.UserID != "" {
			parts = append(parts, `(server_id IS NULL AND user_id = ?)`)
			args = append(args, q.Scope.UserID)
		}
		if len(parts) == 0 {
			return []*AuditEntry{}, 0, nil
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	if q.ServerID != "" {
		conds = append(conds, "server_id = ?")
		args = append(args, q.ServerID)
	}
	if q.UserID != "" {
		conds = append(conds, "user_id = ?")
		args = append(args, q.UserID)
	}
	if q.ActionPrefix != "" {
		conds = append(conds, `action LIKE ? ESCAPE '\'`)
		args = append(args, likePrefix(q.ActionPrefix))
	}
	if !q.Since.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, sqliteTime(q.Since))
	}
	if !q.Until.IsZero() {
		conds = append(conds, "created_at < ?")
		args = append(args, sqliteTime(q.Until))
	}
	if q.Before > 0 {
		conds = append(conds, "id < ?")
		args = append(args, q.Before)
	}
	query := `SELECT id, user_id, api_key_id, mcp_grant_id, server_id, server_name, action, detail, ip_address, created_at FROM audit_log`
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	// id order is insertion order, which is what a stable cursor needs;
	// created_at has one-second resolution and ties.
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.APIKeyID, &e.MCPGrantID, &e.ServerID, &e.ServerName, &e.Action, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].ID
	}
	return entries, next, nil
}

// placeholders returns "?,?,…" with n marks.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// likePrefix escapes LIKE metacharacters so a user-supplied prefix matches
// literally.
func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}

// sqliteTime renders t the way CURRENT_TIMESTAMP stores it (UTC, second
// resolution), so comparisons against DEFAULT CURRENT_TIMESTAMP columns are
// lexically correct.
func sqliteTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// LoginDeviceHistory reports whether the user has any recorded sign-in, and
// whether one came from this device fingerprint.
func (s *Store) LoginDeviceHistory(ctx context.Context, userID, device string) (hasLogins, seen bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) > 0,
		        COALESCE(SUM(json_extract(detail, '$.device') = ?), 0) > 0
		   FROM audit_log WHERE user_id = ? AND action = 'auth.login'`,
		device, userID,
	).Scan(&hasLogins, &seen)
	return hasLogins, seen, err
}
