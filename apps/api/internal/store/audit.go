package store

import (
	"context"
	"encoding/json"
	"time"
)

// ── Audit Log ────────────────────────────────────────────────────

type AuditEntry struct {
	ID        int64     `json:"id"`
	UserID    *string   `json:"user_id"`
	ServerID  *string   `json:"server_id"`
	Action    string    `json:"action"`
	Detail    *string   `json:"detail"`
	IPAddress *string   `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) LogAction(ctx context.Context, userID, serverID, action, ip string, detail any) {
	d, _ := json.Marshal(detail)
	var uid, sid *string
	if userID != "" {
		uid = &userID
	}
	if serverID != "" {
		sid = &serverID
	}
	s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, server_id, action, detail, ip_address) VALUES (?,?,?,?,?)`,
		uid, sid, action, string(d), ip,
	)
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
	q := `SELECT id, user_id, server_id, action, detail, ip_address, created_at FROM audit_log`
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
		if err := rows.Scan(&e.ID, &e.UserID, &e.ServerID, &e.Action, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
