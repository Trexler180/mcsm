package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── Refresh Tokens ───────────────────────────────────────────────

// CreateRefreshToken stores a new session and returns its id. userAgent and ip
// are recorded so the user can later identify the session; pass "" when unknown.
func (s *Store) CreateRefreshToken(ctx context.Context, userID, tokenHash, userAgent, ip string, expiresAt time.Time) (string, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, user_agent, ip, last_used_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)`,
		id, userID, tokenHash, nullIfEmpty(userAgent), nullIfEmpty(ip), expiresAt,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var rt RefreshToken
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at FROM refresh_tokens WHERE token_hash = ?`,
		tokenHash,
	).Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("refresh token not found or expired")
	}
	if err != nil {
		return nil, err
	}
	if !rt.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("refresh token not found or expired")
	}
	return &rt, nil
}

func (s *Store) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteRefreshTokenByID(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteRefreshTokensForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, userID)
	return err
}
