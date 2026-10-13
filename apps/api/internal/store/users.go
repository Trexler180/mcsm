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
		id, NormalizeEmail(email), passwordHash, role,
	)
	if isUniqueEmailViolation(err) {
		return nil, ErrEmailTaken
	}
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
		NormalizeEmail(email),
	).Scan(&u.ID, &u.Email, &hash, &u.DisplayName, &u.Role, &u.CreatedAt, &u.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("user %w", ErrNotFound)
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
		return nil, fmt.Errorf("user %w", ErrNotFound)
	}
	return &u, err
}

func (s *Store) GetUserByEmailInsensitive(ctx context.Context, email string) (*User, error) {
	needle := strings.ToLower(strings.TrimSpace(email))
	if needle == "" {
		return nil, fmt.Errorf("user %w", ErrNotFound)
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
		return nil, fmt.Errorf("user %w", ErrNotFound)
	case 1:
		return matches[0], nil
	default:
		return nil, ErrAmbiguousUserEmail
	}
}

// GetUserPasswordHash reads a user's password hash by id, for step-up
// reauthentication on an already-authenticated session (issuing or rotating an
// access key). The hash never leaves the auth layer.
func (s *Store) GetUserPasswordHash(ctx context.Context, id string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("user %w", ErrNotFound)
	}
	return hash, err
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

// NormalizeEmail is the stored form of an address: trimmed and lowercased.
// Addresses are matched case-insensitively and the column has a unique index
// on lower(email), so every write goes through here.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ErrEmailTaken is returned when an address is already in use.
var ErrEmailTaken = errors.New("a user with that email already exists")

// ErrLastAdmin refuses a change that would leave the panel with no
// administrator.
var ErrLastAdmin = errors.New("this is the last administrator")

// UserAdminUpdate is an administrator's edit of another account. Nil fields are
// left alone.
type UserAdminUpdate struct {
	DisplayName  *string // "" clears it
	Role         *string
	PasswordHash *string
	ResetMFA     bool
}

// UpdateUserAdmin applies an administrator's edit in one transaction, so a
// request either lands whole or not at all. Changing the password, the role,
// or the second factor ends every session the account has: those are the
// changes made when an account is thought compromised, and a demotion should
// not linger in an open tab.
func (s *Store) UpdateUserAdmin(ctx context.Context, id string, u UserAdminUpdate) (endedSessions bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("user %w", ErrNotFound)
		}
		return false, err
	}
	if u.DisplayName != nil {
		var v any
		if *u.DisplayName != "" {
			v = *u.DisplayName
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = ? WHERE id = ?`, v, id); err != nil {
			return false, err
		}
	}
	roleChanged := u.Role != nil && *u.Role != role
	if roleChanged {
		if role == "admin" {
			var admins int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
				return false, err
			}
			if admins <= 1 {
				return false, ErrLastAdmin
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, *u.Role, id); err != nil {
			return false, err
		}
	}
	if u.PasswordHash != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, *u.PasswordHash, id); err != nil {
			return false, err
		}
	}
	if u.ResetMFA {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET totp_secret = NULL, totp_enabled = 0, totp_recovery = NULL WHERE id = ?`, id); err != nil {
			return false, err
		}
	}
	if roleChanged || u.PasswordHash != nil || u.ResetMFA {
		if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, id); err != nil {
			return false, err
		}
		endedSessions = true
	}
	return endedSessions, tx.Commit()
}

// CountAdmins returns how many accounts hold the global admin role.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

// isUniqueEmailViolation recognizes the users email unique index failing.
func isUniqueEmailViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") && strings.Contains(msg, "users")
}
