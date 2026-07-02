package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mcsm/api/internal/auth"
)

// ── Users ────────────────────────────────────────────────────────

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (*User, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, role) VALUES (?, ?, ?, ?)`,
		id, email, passwordHash, role,
	)
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	var u User
	var hash string
	// Email is matched case-insensitively and trimmed: addresses are not
	// case-sensitive, and treating them so here only produced confusing
	// "invalid credentials" failures when a login form capitalised or padded
	// the address.
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, display_name, role, created_at, last_login FROM users WHERE lower(email) = ?`,
		strings.ToLower(strings.TrimSpace(email)),
	).Scan(&u.ID, &u.Email, &hash, &u.DisplayName, &u.Role, &u.CreatedAt, &u.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("user not found")
	}
	return &u, hash, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, role, created_at, last_login FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &u.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user not found")
	}
	return &u, err
}

func (s *Store) GetUserByEmailInsensitive(ctx context.Context, email string) (*User, error) {
	needle := strings.ToLower(strings.TrimSpace(email))
	if needle == "" {
		return nil, fmt.Errorf("user not found")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, display_name, role, created_at, last_login
		 FROM users WHERE lower(email) = ? ORDER BY created_at DESC`,
		needle,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &u.LastLogin); err != nil {
			return nil, err
		}
		matches = append(matches, &u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("user not found")
	case 1:
		return matches[0], nil
	default:
		return nil, ErrAmbiguousUserEmail
	}
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, display_name, role, created_at, last_login FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &u.LastLogin); err != nil {
			return nil, err
		}
		users = append(users, &u)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUserLastLogin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	return err
}

func (s *Store) UpdateUser(ctx context.Context, id string, displayName *string, role string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = ?, role = ? WHERE id = ?`,
		displayName, role, id)
	return err
}

func (s *Store) EnsureAdminUser(ctx context.Context, email, password string) (*User, error) {
	user, hash, err := s.GetUserByEmail(ctx, email)
	if err == nil {
		// Only rewrite the password when it actually changed. Dev mode re-runs
		// this on every boot; rehashing unconditionally would call
		// DeleteRefreshTokensForUser each time and invalidate the browser's
		// session, forcing a fresh login after every restart. Skipping the
		// no-op keeps existing sessions alive across restarts.
		if !auth.CheckPassword(hash, password) {
			newHash, err := auth.HashPassword(password)
			if err != nil {
				return nil, err
			}
			if err := s.UpdateUserPassword(ctx, user.ID, newHash); err != nil {
				return nil, err
			}
			_ = s.DeleteRefreshTokensForUser(ctx, user.ID)
		}
		if user.Role != "admin" {
			if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE id = ?`, user.ID); err != nil {
				return nil, err
			}
			return s.GetUserByID(ctx, user.ID)
		}
		return user, nil
	}
	hash, err = auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	return s.CreateUser(ctx, email, hash, "admin")
}

// ErrUserOwnsServers is returned by DeleteUser when the user still owns one or
// more servers. Rather than cascade-delete real server data out from under an
// admin, we refuse and let the caller reassign or delete those servers first.
var ErrUserOwnsServers = errors.New("user still owns servers")

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	// servers.owner_id is NOT NULL with no ON DELETE action, so a user who owns
	// servers can't be removed. Turn that raw FK failure into a clear, typed
	// error the handler can surface as a 409. Other user references are handled
	// by ON DELETE actions (SET NULL for audit_log/backups, CASCADE for
	// scheduled_tasks/refresh_tokens/api_keys/server_permissions/notifications).
	var owned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE owner_id = ?`, id).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return fmt.Errorf("%w (%d)", ErrUserOwnsServers, owned)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
