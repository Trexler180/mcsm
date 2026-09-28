package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// ── Audit Log ────────────────────────────────────────────────────

type AuditEntry struct {
	ID       int64   `json:"id"`
	UserID   *string `json:"user_id"`
	ServerID *string `json:"server_id"`
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
		`INSERT INTO audit_log (user_id, api_key_id, mcp_grant_id, server_id, action, detail, ip_address) VALUES (?,?,?,?,?,?,?)`,
		uid, kid, gid, sid, action, string(d), ip,
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
	q := `SELECT id, user_id, api_key_id, mcp_grant_id, server_id, action, detail, ip_address, created_at FROM audit_log`
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
		if err := rows.Scan(&e.ID, &e.UserID, &e.APIKeyID, &e.MCPGrantID, &e.ServerID, &e.Action, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
