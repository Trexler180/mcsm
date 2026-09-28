package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mcsm/api/internal/auth"
)

// ── Agent access keys ────────────────────────────────────────────
//
// A scoped, expiring machine credential. Operator-controlled automation
// presents it as `Authorization: Bearer mcsm_pat_…` and acts as its owner,
// bounded by an explicit server allowlist and permission scopes. Nothing here
// grants access on its own: the bounds are intersected with the owner's live
// per-server permissions on every request (see serverAccessGate).
//
// The raw secret exists in exactly two places: the create/rotate response, and
// whatever secret store the operator puts it in. The database holds only its
// SHA-256 and a short non-secret display prefix.

const (
	// MaxAccessKeyLifetime bounds how far out an expiry may be set. Centralized
	// so production policy can be tightened in one place.
	MaxAccessKeyLifetime = 90 * 24 * time.Hour

	// accessKeyUsageStale is how stale last_used_at must be before a request
	// rewrites it. Without this, a polling agent would write a row per call and
	// grow the WAL for no operational benefit.
	accessKeyUsageStale = 5 * time.Minute

	// accessKeySecretBytes is the entropy behind each token (256 bits).
	accessKeySecretBytes = 32

	// accessKeyPrefixChars is how much of the encoded secret the non-secret
	// display prefix keeps. Enough to match a key in the UI against the one an
	// agent holds; far too little to brute-force the remainder.
	accessKeyPrefixChars = 8
)

var (
	// ErrAccessKeyInvalid is the single, uniform failure for every unusable
	// credential state — unknown, expired, revoked, orphaned, or malformed.
	// Callers must not distinguish them to the client.
	ErrAccessKeyInvalid = errors.New("invalid access key")

	ErrAccessKeyNotFound      = errors.New("access key not found")
	ErrAccessKeyNameRequired  = errors.New("access key name is required")
	ErrAccessKeyNoScopes      = errors.New("at least one scope is required")
	ErrAccessKeyNoServers     = errors.New("at least one server is required")
	ErrAccessKeyAdminScope    = errors.New("the admin scope cannot be granted to an access key")
	ErrAccessKeyExpiryMissing = errors.New("an expiry is required")
	ErrAccessKeyExpiryPast    = errors.New("expiry must be in the future")
	ErrAccessKeyExpiryTooFar  = errors.New("expiry must be within 90 days")
	ErrAccessKeyRevoked       = errors.New("access key is revoked")
)

// APIKey is an access key's metadata. It deliberately has no field for the
// token hash or the raw secret: this struct is serialized straight to the
// owner's Security page, so the type itself makes disclosure impossible.
type APIKey struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	// TokenPrefix is the non-secret leading fragment, e.g. "mcsm_pat_AbCdEf12".
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	ServerIDs   []string   `json:"server_ids"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastUsedIP  *string    `json:"last_used_ip"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// Active reports whether the key would authenticate right now.
func (k *APIKey) Active(now time.Time) bool {
	if k == nil || k.RevokedAt != nil {
		return false
	}
	if k.Expired(now) {
		return false
	}
	return len(k.Scopes) > 0 && len(k.ServerIDs) > 0
}

// Expired reports whether the key's bounded lifetime has run out. A missing
// expiry counts as expired: expiry is mandatory for every key this API issues,
// so a NULL can only be a pre-migration row, and those must authenticate as
// nothing rather than as a key that never dies.
func (k *APIKey) Expired(now time.Time) bool {
	if k == nil {
		return true
	}
	return k.ExpiresAt == nil || !k.ExpiresAt.After(now)
}

// AuthenticatedAccessKey pairs a valid key with its live owner row. The router
// adapts it into the auth package's machine identity.
type AuthenticatedAccessKey struct {
	Key  *APIKey
	User *User
}

// GenerateAccessKeyToken mints a fresh secret. It returns the raw token (shown
// once, never stored), the SHA-256 of the full presented string, and the
// non-secret display prefix.
func GenerateAccessKeyToken() (token, hash, prefix string, err error) {
	b := make([]byte, accessKeySecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("access key rand: %w", err)
	}
	// URL-safe, unpadded: safe to paste into an env var, a header, or a shell
	// without quoting surprises.
	secret := base64.RawURLEncoding.EncodeToString(b)
	token = auth.AccessKeyPrefix + secret
	hash = HashAccessKey(token)
	prefix = auth.AccessKeyPrefix + secret[:accessKeyPrefixChars]
	return token, hash, prefix, nil
}

// HashAccessKey hashes the full presented token. Access keys are high-entropy
// random strings, so a fast SHA-256 is sufficient — the same reasoning that
// applies to refresh tokens and recovery codes, and not to human passwords.
func HashAccessKey(token string) string {
	return sha256Hex(token)
}

// NormalizeAccessKeyScopes validates, de-dupes, and sorts a key's scopes using
// the same permission vocabulary collaborators are granted, then refuses the
// two things a key must never carry: nothing at all, and `admin`.
//
// Refusing `admin` is what keeps membership administration, server deletion,
// and the clone/create paths out of reach even for an admin-owned key.
func NormalizeAccessKeyScopes(scopes []string) ([]string, error) {
	for _, s := range scopes {
		if strings.ToLower(strings.TrimSpace(s)) == string(ServerPermissionAdmin) {
			return nil, ErrAccessKeyAdminScope
		}
	}
	normalized, err := NormalizeServerPermissions(scopes)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, ErrAccessKeyNoScopes
	}
	return normalized, nil
}

// ValidateAccessKeyExpiry enforces the mandatory bounded lifetime. There is no
// "never expires" option by design.
func ValidateAccessKeyExpiry(expiresAt time.Time, now time.Time) error {
	if expiresAt.IsZero() {
		return ErrAccessKeyExpiryMissing
	}
	if !expiresAt.After(now) {
		return ErrAccessKeyExpiryPast
	}
	if expiresAt.After(now.Add(MaxAccessKeyLifetime)) {
		return ErrAccessKeyExpiryTooFar
	}
	return nil
}

// normalizeAccessKeyServers de-dupes and orders a server allowlist. Existence
// is checked separately (against the caller's own access), so this is purely
// shape validation.
func normalizeAccessKeyServers(serverIDs []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(serverIDs))
	for _, id := range serverIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, ErrAccessKeyNoServers
	}
	return out, nil
}

// CreateAccessKey issues a key for a user and returns it together with the raw
// token — the only moment that secret exists outside the caller's own store.
//
// Authorization (does this user actually hold these scopes on these servers?)
// belongs to the handler, which knows the request context; this method owns
// shape, normalization, and lifetime.
func (s *Store) CreateAccessKey(ctx context.Context, userID, name string, scopes, serverIDs []string, expiresAt time.Time) (*APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", ErrAccessKeyNameRequired
	}
	if len(name) > 100 {
		name = name[:100]
	}
	normScopes, err := NormalizeAccessKeyScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	normServers, err := normalizeAccessKeyServers(serverIDs)
	if err != nil {
		return nil, "", err
	}
	if err := ValidateAccessKeyExpiry(expiresAt, time.Now()); err != nil {
		return nil, "", err
	}

	token, hash, prefix, err := GenerateAccessKeyToken()
	if err != nil {
		return nil, "", err
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, user_id, token_hash, token_prefix, name, scopes, server_ids, expires_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, userID, hash, prefix, name, strArray(normScopes), strArray(normServers), expiresAt.UTC(),
	); err != nil {
		return nil, "", err
	}
	key, err := s.GetAccessKey(ctx, id, userID)
	if err != nil {
		return nil, "", err
	}
	return key, token, nil
}

const accessKeyColumns = `id, user_id, name, COALESCE(token_prefix,''), scopes, server_ids,
	 expires_at, created_at, last_used_at, last_used_ip, revoked_at`

func scanAccessKey(scan func(dest ...any) error) (*APIKey, error) {
	var k APIKey
	var scopes, servers strArray
	if err := scan(&k.ID, &k.UserID, &k.Name, &k.TokenPrefix, &scopes, &servers,
		&k.ExpiresAt, &k.CreatedAt, &k.LastUsedAt, &k.LastUsedIP, &k.RevokedAt); err != nil {
		return nil, err
	}
	k.Scopes = []string(scopes)
	k.ServerIDs = []string(servers)
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	if k.ServerIDs == nil {
		k.ServerIDs = []string{}
	}
	return &k, nil
}

// ListAccessKeys returns a user's keys, newest first, including revoked ones so
// the owner keeps a record of what existed.
func (s *Store) ListAccessKeys(ctx context.Context, userID string) ([]*APIKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accessKeyColumns+` FROM api_keys WHERE user_id = ? ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		k, err := scanAccessKey(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetAccessKey reads one key, scoped to its owner — a key id belonging to
// another user is indistinguishable from one that does not exist.
func (s *Store) GetAccessKey(ctx context.Context, id, userID string) (*APIKey, error) {
	k, err := scanAccessKey(s.db.QueryRowContext(ctx,
		`SELECT `+accessKeyColumns+` FROM api_keys WHERE id = ? AND user_id = ?`, id, userID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccessKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

// RotateAccessKey replaces a key's secret in place and returns the new raw
// token. The swap is a single conditional UPDATE, so the old token stops
// working the instant it commits — there is no window where both are valid.
// Stale usage metadata is cleared: it described the previous credential.
func (s *Store) RotateAccessKey(ctx context.Context, id, userID string) (*APIKey, string, error) {
	token, hash, prefix, err := GenerateAccessKeyToken()
	if err != nil {
		return nil, "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE api_keys
		    SET token_hash = ?, token_prefix = ?, last_used_at = NULL, last_used_ip = NULL
		  WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		hash, prefix, id, userID)
	if err != nil {
		return nil, "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either it isn't theirs, doesn't exist, or is already revoked. Tell
		// those apart only well enough for a useful message to the owner.
		if existing, gerr := s.GetAccessKey(ctx, id, userID); gerr == nil && existing.RevokedAt != nil {
			return nil, "", ErrAccessKeyRevoked
		}
		return nil, "", ErrAccessKeyNotFound
	}
	key, err := s.GetAccessKey(ctx, id, userID)
	if err != nil {
		return nil, "", err
	}
	return key, token, nil
}

// RevokeAccessKey soft-revokes a key. Idempotent: revoking an already-revoked
// key succeeds without changing revoked_at, so a retried request is safe. The
// row survives so audit history keeps naming a real credential.
func (s *Store) RevokeAccessKey(ctx context.Context, id, userID string) error {
	if _, err := s.GetAccessKey(ctx, id, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, id, userID)
	return err
}

// AuthenticateAccessKey resolves a presented bearer token to its key and live
// owner. Every rejection returns ErrAccessKeyInvalid — the caller must not let
// an attacker tell "no such key" from "expired" from "revoked".
//
// This is deliberately uncached. Immediate revocation is a required invariant,
// and a cache would buy latency at the cost of a window where a revoked key
// still works.
func (s *Store) AuthenticateAccessKey(ctx context.Context, presented string) (*AuthenticatedAccessKey, error) {
	if !strings.HasPrefix(presented, auth.AccessKeyPrefix) || len(presented) <= len(auth.AccessKeyPrefix) {
		return nil, ErrAccessKeyInvalid
	}

	k, err := scanAccessKey(s.db.QueryRowContext(ctx,
		`SELECT `+accessKeyColumns+` FROM api_keys WHERE token_hash = ?`, HashAccessKey(presented)).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccessKeyInvalid
	}
	if err != nil {
		// Malformed JSON in scopes/server_ids lands here too, as does a database
		// failure. Both fail closed; the detail stays server-side.
		slog.Warn("access key lookup failed", "error", err)
		return nil, ErrAccessKeyInvalid
	}

	if !k.Active(time.Now()) {
		return nil, ErrAccessKeyInvalid
	}
	// Re-normalize what was stored. A row edited outside the API — or written by
	// an older, looser version — must not widen a key's authority.
	scopes, err := NormalizeAccessKeyScopes(k.Scopes)
	if err != nil {
		slog.Warn("access key has unusable scopes", "key_id", k.ID, "error", err)
		return nil, ErrAccessKeyInvalid
	}
	k.Scopes = scopes

	user, err := s.GetUserByID(ctx, k.UserID)
	if err != nil || user == nil {
		// The owner is gone (or unreadable): the key acts as nobody.
		return nil, ErrAccessKeyInvalid
	}
	return &AuthenticatedAccessKey{Key: k, User: user}, nil
}

// AccessKeyStillValid reports whether the credential a long-lived connection
// opened with would still authenticate now: the same key with the same secret
// (a rotation retires the old one), active, and owned by a live user. It is the
// re-check a WebSocket runs while it stays open, and it fails closed on any
// error.
func (s *Store) AccessKeyStillValid(ctx context.Context, keyID, tokenHash string) bool {
	if keyID == "" || tokenHash == "" {
		return false
	}
	k, err := scanAccessKey(s.db.QueryRowContext(ctx,
		`SELECT `+accessKeyColumns+` FROM api_keys WHERE id = ? AND token_hash = ?`, keyID, tokenHash).Scan)
	if err != nil || !k.Active(time.Now()) {
		return false
	}
	user, err := s.GetUserByID(ctx, k.UserID)
	return err == nil && user != nil
}

// TouchAccessKey records that a key was just used, but only when the stored
// metadata is actually stale or the source IP changed. Sustained polling
// therefore costs no writes: the common case is an in-memory comparison against
// the row we already read during authentication.
func (s *Store) TouchAccessKey(ctx context.Context, k *APIKey, ip string) error {
	if k == nil {
		return nil
	}
	now := time.Now()
	fresh := k.LastUsedAt != nil && now.Sub(*k.LastUsedAt) < accessKeyUsageStale
	sameIP := (k.LastUsedIP == nil && ip == "") || (k.LastUsedIP != nil && *k.LastUsedIP == ip)
	if fresh && sameIP {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ?, last_used_ip = ? WHERE id = ?`,
		now.UTC(), nullIfEmpty(ip), k.ID)
	return err
}
