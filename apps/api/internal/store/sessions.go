package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrRefreshTokenAlreadyRotated = errors.New("refresh token already rotated")

// ErrRefreshTokenReused means a refresh token was presented after it had been
// rotated away, outside the grace window. Only a copy of the token can do
// that, so the caller ends the session.
var ErrRefreshTokenReused = errors.New("refresh token reused after rotation")

// RefreshReuseGrace is how long a just-rotated refresh token still earns an
// access token. Tabs share one cookie jar, so two of them refreshing together
// present the same token; the one that loses the race is a retry, not theft.
const RefreshReuseGrace = 30 * time.Second

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
func (s *Store) RotateRefreshToken(ctx context.Context, id, currentHash, newHash, ip, userAgent string, expiresAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens
		    SET token_hash = ?, previous_hash = token_hash, rotated_at_unix = ?,
		        ip = ?, user_agent = ?, last_used_at = CURRENT_TIMESTAMP, expires_at = ?
		  WHERE id = ? AND token_hash = ?`,
		newHash, time.Now().Unix(), nullIfEmpty(ip), nullIfEmpty(userAgent), expiresAt, id, currentHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrRefreshTokenAlreadyRotated
	}
	return nil
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

// LookupRefreshToken resolves a presented refresh token. grace is true when the
// token was rotated away within RefreshReuseGrace: the caller may issue an
// access token for the session but must not rotate it again. A token rotated
// away earlier than that returns the session with ErrRefreshTokenReused.
func (s *Store) LookupRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, bool, error) {
	if rt, err := s.GetRefreshToken(ctx, tokenHash); err == nil {
		return rt, false, nil
	}
	var rt RefreshToken
	var rotatedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, rotated_at_unix FROM refresh_tokens WHERE previous_hash = ?`,
		tokenHash,
	).Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rotatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("refresh token not found or expired")
	}
	if err != nil {
		return nil, false, err
	}
	if !rt.ExpiresAt.After(time.Now()) {
		return nil, false, fmt.Errorf("refresh token not found or expired")
	}
	if rotatedAt.Valid && time.Since(time.Unix(rotatedAt.Int64, 0)) <= RefreshReuseGrace {
		return &rt, true, nil
	}
	return &rt, false, ErrRefreshTokenReused
}

// SessionActive reports whether a session (a refresh-token row) still exists
// for the user and has not expired. Access tokens are bound to a session, and
// the auth middleware asks this on every request, so ending a session — or
// deleting the user, which cascades — ends its access tokens immediately.
func (s *Store) SessionActive(ctx context.Context, sessionID, userID string) (bool, error) {
	var expires time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT expires_at FROM refresh_tokens WHERE id = ? AND user_id = ?`, sessionID, userID,
	).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expires.After(time.Now()), nil
}
