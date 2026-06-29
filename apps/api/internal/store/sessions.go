package store

import (
	"context"
	"time"
)

// Session is one active refresh-token session, surfaced to the user so they can
// review where they're logged in and revoke individual ones. The token itself is
// never included.
type Session struct {
	ID         string     `json:"id"`
	IP         string     `json:"ip"`
	UserAgent  string     `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Current    bool       `json:"current"`
}

// ListSessions returns a user's unexpired sessions, most-recently-active first.
// Expiry is evaluated in Go rather than SQL: the driver stores time.Time in a
// format that doesn't compare reliably against SQLite's CURRENT_TIMESTAMP, the
// same reason GetRefreshToken checks expiry in Go.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(ip,''), COALESCE(user_agent,''), created_at, last_used_at, expires_at
		   FROM refresh_tokens
		  WHERE user_id = ?
		  ORDER BY COALESCE(last_used_at, created_at) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []*Session{}
	for rows.Next() {
		var ses Session
		if err := rows.Scan(&ses.ID, &ses.IP, &ses.UserAgent, &ses.CreatedAt, &ses.LastUsedAt, &ses.ExpiresAt); err != nil {
			return nil, err
		}
		if !ses.ExpiresAt.After(now) {
			continue
		}
		out = append(out, &ses)
	}
	return out, rows.Err()
}

// RotateRefreshToken replaces a session's token hash (and refreshes its expiry
// and device metadata) in place, preserving the session id and created_at so it
// stays a single session across refreshes.
func (s *Store) RotateRefreshToken(ctx context.Context, id, newHash, ip, userAgent string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens
		    SET token_hash = ?, ip = ?, user_agent = ?, last_used_at = CURRENT_TIMESTAMP, expires_at = ?
		  WHERE id = ?`,
		newHash, nullIfEmpty(ip), nullIfEmpty(userAgent), expiresAt, id)
	return err
}

// TouchSession records that a session was just used (refresh), updating its IP
// and user agent so the list reflects the latest device.
func (s *Store) TouchSession(ctx context.Context, id, ip, userAgent string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET last_used_at = CURRENT_TIMESTAMP, ip = ?, user_agent = ? WHERE id = ?`,
		ip, userAgent, id)
	return err
}

// DeleteSessionForUser revokes one session, scoped to its owner so a user can
// only revoke their own. Returns whether a row was removed.
func (s *Store) DeleteSessionForUser(ctx context.Context, id, userID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteSessionsForUserExcept revokes every session for a user except keepID
// (used for "log out all other sessions").
func (s *Store) DeleteSessionsForUserExcept(ctx context.Context, userID, keepID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = ? AND id <> ?`, userID, keepID)
	return err
}
