package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PlayerSession is one player visit on one server, tracked by the poller at
// about one-minute resolution. EndedAt nil means the player is on right now.
type PlayerSession struct {
	ID         string     `json:"id"`
	ServerID   string     `json:"server_id"`
	PlayerName string     `json:"player_name"`
	PlayerUUID string     `json:"player_uuid,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
}

// sqlTime renders a timestamp in the same format SQLite's CURRENT_TIMESTAMP
// uses, so string comparison in WHERE clauses stays correct (the convention
// HasRecentLifecycleAction established).
func sqlTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// StartPlayerSession opens a session and returns its id.
func (s *Store) StartPlayerSession(ctx context.Context, serverID, name, playerUUID string, startedAt time.Time) (string, error) {
	id := uuid.New().String()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO player_sessions (id, server_id, player_name, player_uuid, started_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, serverID, name, playerUUID, sqlTime(startedAt))
	return id, err
}

// EndPlayerSession closes one session (no-op if already closed).
func (s *Store) EndPlayerSession(ctx context.Context, id string, endedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE player_sessions SET ended_at = ? WHERE id = ? AND ended_at IS NULL`,
		sqlTime(endedAt), id)
	return err
}

// EndAllPlayerSessions closes every open session on a server (server stopped,
// became unreachable, or the API is adopting state after a restart).
func (s *Store) EndAllPlayerSessions(ctx context.Context, serverID string, endedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE player_sessions SET ended_at = ? WHERE server_id = ? AND ended_at IS NULL`,
		sqlTime(endedAt), serverID)
	return err
}

// OpenPlayerSessions returns the sessions currently open on a server.
func (s *Store) OpenPlayerSessions(ctx context.Context, serverID string) ([]PlayerSession, error) {
	return s.queryPlayerSessions(ctx, `
		SELECT id, server_id, player_name, player_uuid, started_at, ended_at
		FROM player_sessions WHERE server_id = ? AND ended_at IS NULL`, serverID)
}

// ListPlayerSessions returns a server's sessions started since a cutoff,
// newest first, optionally filtered to one player (case-insensitive name).
func (s *Store) ListPlayerSessions(ctx context.Context, serverID, playerName string, since time.Time, limit int) ([]PlayerSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `
		SELECT id, server_id, player_name, player_uuid, started_at, ended_at
		FROM player_sessions
		WHERE server_id = ? AND started_at >= ?`
	args := []any{serverID, sqlTime(since)}
	if playerName != "" {
		q += ` AND player_name = ? COLLATE NOCASE`
		args = append(args, playerName)
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	return s.queryPlayerSessions(ctx, q, args...)
}

// PlaytimeFromSessions sums the in-window play seconds across sessions: each
// session counts from max(start, since) to its end, or to now while open.
func PlaytimeFromSessions(sessions []PlayerSession, since, now time.Time) int64 {
	var total int64
	for _, ps := range sessions {
		start := ps.StartedAt
		if start.Before(since) {
			start = since
		}
		end := now
		if ps.EndedAt != nil {
			end = *ps.EndedAt
		}
		if d := end.Sub(start); d > 0 {
			total += int64(d.Seconds())
		}
	}
	return total
}

func (s *Store) queryPlayerSessions(ctx context.Context, q string, args ...any) ([]PlayerSession, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PlayerSession, 0, 32)
	for rows.Next() {
		var ps PlayerSession
		if err := rows.Scan(&ps.ID, &ps.ServerID, &ps.PlayerName, &ps.PlayerUUID, &ps.StartedAt, &ps.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}
