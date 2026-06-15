package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ── Mod conflicts ────────────────────────────────────────────────

// ModConflict is a persisted, detected mod conflict on a server. It is active
// while ResolvedAt is nil.
type ModConflict struct {
	ID         string     `json:"id"`
	ServerID   string     `json:"server_id"`
	Kind       string     `json:"kind"`
	Summary    string     `json:"summary"`
	Mods       []string   `json:"mods"`
	DetectedAt time.Time  `json:"detected_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

// RecordConflict inserts a new active conflict, or refreshes the existing active
// one for the server when an identical summary is already open (so repeated
// detections of the same crash don't pile up). Returns the conflict id.
func (s *Store) RecordConflict(ctx context.Context, serverID, kind, summary string, mods []string) (string, error) {
	var existingID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM mod_conflicts WHERE server_id = ? AND resolved_at IS NULL AND summary = ? LIMIT 1`,
		serverID, summary,
	).Scan(&existingID)
	if err == nil && existingID != "" {
		_, err = s.db.ExecContext(ctx,
			`UPDATE mod_conflicts SET detected_at = CURRENT_TIMESTAMP WHERE id = ?`, existingID)
		return existingID, err
	}

	modsJSON, _ := json.Marshal(mods)
	id := uuid.NewString()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO mod_conflicts (id, server_id, kind, summary, mods) VALUES (?,?,?,?,?)`,
		id, serverID, kind, summary, string(modsJSON),
	)
	return id, err
}

// ResolveServerConflicts marks all active conflicts for a server resolved. Used
// when the offending jars have been disabled.
func (s *Store) ResolveServerConflicts(ctx context.Context, serverID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE mod_conflicts SET resolved_at = CURRENT_TIMESTAMP WHERE server_id = ? AND resolved_at IS NULL`,
		serverID,
	)
	return err
}

// ListConflicts returns conflicts for a server, newest first. When activeOnly is
// true only unresolved conflicts are returned.
func (s *Store) ListConflicts(ctx context.Context, serverID string, activeOnly bool) ([]*ModConflict, error) {
	q := `SELECT id, server_id, kind, summary, mods, detected_at, resolved_at FROM mod_conflicts WHERE server_id = ?`
	if activeOnly {
		q += ` AND resolved_at IS NULL`
	}
	q += ` ORDER BY detected_at DESC`
	rows, err := s.db.QueryContext(ctx, q, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflicts(rows)
}

// ListActiveConflicts returns every unresolved conflict across all servers,
// newest first — the cockpit's "active conflicts" feed.
func (s *Store) ListActiveConflicts(ctx context.Context) ([]*ModConflict, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, kind, summary, mods, detected_at, resolved_at
		   FROM mod_conflicts WHERE resolved_at IS NULL ORDER BY detected_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflicts(rows)
}

func scanConflicts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*ModConflict, error) {
	out := []*ModConflict{}
	for rows.Next() {
		var c ModConflict
		var modsJSON string
		if err := rows.Scan(&c.ID, &c.ServerID, &c.Kind, &c.Summary, &modsJSON, &c.DetectedAt, &c.ResolvedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(modsJSON), &c.Mods)
		if c.Mods == nil {
			c.Mods = []string{}
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// HasRecentLifecycleAction reports whether a panel-initiated lifecycle action
// (start/stop/restart/kill/reinstall) was logged for the server within the
// given window. The poller uses it to avoid mislabelling a restart's brief
// offline blip — or a just-issued stop — as a crash.
func (s *Store) HasRecentLifecycleAction(ctx context.Context, serverID string, within time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-within).Format("2006-01-02 15:04:05")
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM audit_log
		   WHERE server_id = ?
		     AND action IN ('server.start','server.stop','server.restart','server.kill','server.reinstall')
		     AND created_at >= ?
		   LIMIT 1`,
		serverID, cutoff,
	).Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ── Log events ───────────────────────────────────────────────────

// LogEvent is an indexed warning/error from a server, surfaced in the cockpit.
type LogEvent struct {
	ID        int64     `json:"id"`
	ServerID  string    `json:"server_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// InsertLogEvent records a single warning/error. Best-effort: callers in hot
// paths (the poller) ignore the error.
func (s *Store) InsertLogEvent(ctx context.Context, serverID, level, message, source string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO server_log_events (server_id, level, message, source) VALUES (?,?,?,?)`,
		serverID, level, message, source,
	)
	return err
}

// ListLogEvents returns recent log events, optionally scoped to one server and
// filtered to a minimum level. limit defaults to 50, capped at 200.
func (s *Store) ListLogEvents(ctx context.Context, serverID, level string, limit int) ([]*LogEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	q := `SELECT id, server_id, level, message, source, created_at FROM server_log_events`
	args := []any{}
	conds := []string{}
	if serverID != "" {
		conds = append(conds, "server_id = ?")
		args = append(args, serverID)
	}
	if level == "error" {
		conds = append(conds, "level = 'error'")
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE " + c
		} else {
			q += " AND " + c
		}
	}
	q += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LogEvent{}
	for rows.Next() {
		var e LogEvent
		if err := rows.Scan(&e.ID, &e.ServerID, &e.Level, &e.Message, &e.Source, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
