package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mcsm/api/internal/auth"
	"github.com/robfig/cron/v3"
)

// NextRunForCron returns the next time a 5-field cron expression fires after
// `from`, or nil when the expression can't be parsed. Uses the same standard
// parser the scheduler runs on, so the stored next_run matches reality.
func NextRunForCron(expr string, from time.Time) *time.Time {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil
	}
	n := sched.Next(from)
	return &n
}

type Store struct {
	db        *sql.DB
	secretKey []byte // AES-256 key for app_secrets; nil disables secret storage
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// WithEncryption sets the master secret used to encrypt app_secrets at rest and
// returns the same store for chaining. An empty master leaves secret storage
// disabled (the secret methods then return a clear error).
func (s *Store) WithEncryption(master string) *Store {
	s.secretKey = deriveKey(master)
	return s
}

// ── Models ───────────────────────────────────────────────────────

type User struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName *string    `json:"display_name"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLogin   *time.Time `json:"last_login"`
}

type ServerPermission string

const (
	// Group permissions. Holding a group grants every leaf beneath it.
	ServerPermissionView     ServerPermission = "view"
	ServerPermissionPower    ServerPermission = "power"
	ServerPermissionConsole  ServerPermission = "console"
	ServerPermissionPlayers  ServerPermission = "players"
	ServerPermissionFiles    ServerPermission = "files"
	ServerPermissionMods     ServerPermission = "mods"
	ServerPermissionBackups  ServerPermission = "backups"
	ServerPermissionTasks    ServerPermission = "tasks"
	ServerPermissionSettings ServerPermission = "settings"
	ServerPermissionAdmin    ServerPermission = "admin"

	// Leaf permissions. Each is "<group>.<action>" and is satisfied by holding
	// either the leaf itself, its parent group, or admin.
	ServerPermissionPowerStart   ServerPermission = "power.start"
	ServerPermissionPowerStop    ServerPermission = "power.stop"
	ServerPermissionPowerRestart ServerPermission = "power.restart"
	ServerPermissionPowerKill    ServerPermission = "power.kill"

	ServerPermissionPlayersWhitelist ServerPermission = "players.whitelist"
	ServerPermissionPlayersKick      ServerPermission = "players.kick"
	ServerPermissionPlayersBan       ServerPermission = "players.ban"
	ServerPermissionPlayersOp        ServerPermission = "players.op"

	ServerPermissionFilesRead   ServerPermission = "files.read"
	ServerPermissionFilesWrite  ServerPermission = "files.write"
	ServerPermissionFilesDelete ServerPermission = "files.delete"

	ServerPermissionModsInstall ServerPermission = "mods.install"
	ServerPermissionModsUpdate  ServerPermission = "mods.update"
	ServerPermissionModsRemove  ServerPermission = "mods.remove"

	ServerPermissionBackupsCreate  ServerPermission = "backups.create"
	ServerPermissionBackupsRestore ServerPermission = "backups.restore"
	ServerPermissionBackupsDelete  ServerPermission = "backups.delete"
)

var (
	ErrInvalidServerPermission = errors.New("invalid server permission")
	ErrServerMemberNotFound    = errors.New("server member not found")
	ErrServerPermissionsStale  = errors.New("server permissions changed")
	ErrAmbiguousUserEmail      = errors.New("multiple users match email")
)

// allServerPermissions is the set of grantable group permissions. Owners and
// global admins are reported as holding all of these; leaves are implied.
var allServerPermissions = []string{
	string(ServerPermissionView),
	string(ServerPermissionPower),
	string(ServerPermissionConsole),
	string(ServerPermissionPlayers),
	string(ServerPermissionFiles),
	string(ServerPermissionMods),
	string(ServerPermissionBackups),
	string(ServerPermissionTasks),
	string(ServerPermissionSettings),
	string(ServerPermissionAdmin),
}

// serverPermissionLeaves maps each group to its fine-grained leaves. Groups not
// listed here (view, console, tasks, settings, admin) are atomic.
var serverPermissionLeaves = map[string][]string{
	string(ServerPermissionPower): {
		string(ServerPermissionPowerStart), string(ServerPermissionPowerStop),
		string(ServerPermissionPowerRestart), string(ServerPermissionPowerKill),
	},
	string(ServerPermissionPlayers): {
		string(ServerPermissionPlayersWhitelist), string(ServerPermissionPlayersKick),
		string(ServerPermissionPlayersBan), string(ServerPermissionPlayersOp),
	},
	string(ServerPermissionFiles): {
		string(ServerPermissionFilesRead), string(ServerPermissionFilesWrite),
		string(ServerPermissionFilesDelete),
	},
	string(ServerPermissionMods): {
		string(ServerPermissionModsInstall), string(ServerPermissionModsUpdate),
		string(ServerPermissionModsRemove),
	},
	string(ServerPermissionBackups): {
		string(ServerPermissionBackupsCreate), string(ServerPermissionBackupsRestore),
		string(ServerPermissionBackupsDelete),
	},
}

// validServerPermissions accepts every group plus every leaf.
var validServerPermissions = func() map[string]bool {
	m := make(map[string]bool, len(allServerPermissions))
	for _, p := range allServerPermissions {
		m[p] = true
	}
	for _, leaves := range serverPermissionLeaves {
		for _, leaf := range leaves {
			m[leaf] = true
		}
	}
	return m
}()

type ServerMember struct {
	ServerID    string   `json:"server_id"`
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	DisplayName *string  `json:"display_name"`
	Role        string   `json:"role"`
	Owner       bool     `json:"owner"`
	Permissions []string `json:"permissions"`
}

func AllServerPermissions() []string {
	out := make([]string, len(allServerPermissions))
	copy(out, allServerPermissions)
	return out
}

// permissionParent returns the group of a leaf permission ("power.start" ->
// "power"), or "" for a group/atomic permission.
func permissionParent(p string) string {
	if i := strings.IndexByte(p, '.'); i >= 0 {
		return p[:i]
	}
	return ""
}

// NormalizeServerPermissions validates, de-dupes, and sorts a permission set.
// Leaves whose parent group is also present are dropped, since the group
// already grants them — this keeps stored sets minimal and comparisons stable.
func NormalizeServerPermissions(perms []string) ([]string, error) {
	seen := map[string]bool{}
	for _, perm := range perms {
		p := strings.ToLower(strings.TrimSpace(perm))
		if p == "" {
			continue
		}
		if !validServerPermissions[p] {
			return nil, fmt.Errorf("%w: %s", ErrInvalidServerPermission, perm)
		}
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for perm := range seen {
		if parent := permissionParent(perm); parent != "" && seen[parent] {
			continue // implied by the group; don't store redundantly
		}
		out = append(out, perm)
	}
	sort.Strings(out)
	return out, nil
}

// HasServerPermission reports whether perms satisfies a specific permission.
// A leaf is satisfied by the leaf itself, its parent group, or admin; a group
// is satisfied by the group or admin.
func HasServerPermission(perms []string, needed ServerPermission) bool {
	need := string(needed)
	// Any granted permission implies the ability to view the server: if you can
	// act on it at all, you can open its dashboard. This avoids a half-state
	// where e.g. power.start is granted but the dashboard itself 403s.
	if need == string(ServerPermissionView) && len(perms) > 0 {
		return true
	}
	parent := permissionParent(need)
	for _, perm := range perms {
		if perm == string(ServerPermissionAdmin) || perm == need {
			return true
		}
		if parent != "" && perm == parent {
			return true
		}
	}
	return false
}

// HasServerGroupAccess reports whether perms grants any access within a group —
// used to gate read/list endpoints that any holder of the group (or any of its
// leaves) should be able to reach.
func HasServerGroupAccess(perms []string, group ServerPermission) bool {
	g := string(group)
	for _, perm := range perms {
		if perm == string(ServerPermissionAdmin) || perm == g || permissionParent(perm) == g {
			return true
		}
	}
	return false
}

func samePermissionSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type Node struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	FQDN      string     `json:"fqdn"`
	Port      int        `json:"port"`
	Scheme    string     `json:"scheme"`
	Token     string     `json:"-"`
	MemoryMb  *int       `json:"memory_mb"`
	DiskGb    *int       `json:"disk_gb"`
	CPUCores  *int       `json:"cpu_cores"`
	Location  *string    `json:"location"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  *time.Time `json:"last_seen"`
}

var ErrNodeHasServers = errors.New("node has servers")

type Server struct {
	ID            string          `json:"id"`
	NodeID        string          `json:"node_id"`
	OwnerID       string          `json:"owner_id"`
	Name          string          `json:"name"`
	Description   *string         `json:"description"`
	Platform      string          `json:"platform"`
	MCVersion     string          `json:"mc_version"`
	LoaderVersion *string         `json:"loader_version"`
	DirectoryPath string          `json:"directory_path"`
	JavaBinary    string          `json:"java_binary"`
	JVMArgs       []string        `json:"jvm_args"`
	Port          int             `json:"port"`
	RAMMbMin      int             `json:"ram_mb_min"`
	RAMMbMax      int             `json:"ram_mb_max"`
	Status        string          `json:"status"`
	AutoStart     bool            `json:"auto_start"`
	Tags          []string        `json:"tags"`
	Settings      json.RawMessage `json:"settings"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type InstalledMod struct {
	ID             string    `json:"id"`
	ServerID       string    `json:"server_id"`
	Source         string    `json:"source"`
	SourceID       *string   `json:"source_id"`
	VersionID      *string   `json:"version_id"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	FileName       string    `json:"file_name"`
	SHA256         *string   `json:"sha256"`
	SHA512         *string   `json:"sha512"`
	Pinned         bool      `json:"pinned"`
	Enabled        bool      `json:"enabled"`
	InstallPath    string    `json:"install_path"`
	InstalledAsDep bool      `json:"installed_as_dep"`
	InstalledAt    time.Time `json:"installed_at"`

	// Derived (not stored): which installed mods require this one, and whether
	// it was auto-installed as a dependency that nothing needs anymore.
	RequiredBy []string `json:"required_by"`
	Orphaned   bool     `json:"orphaned"`
}

// ModDependency is one edge of the reverse-dependency graph: DependentProjectID
// requires DependencyProjectID, on a given server.
type ModDependency struct {
	DependentProjectID  string
	DependencyProjectID string
}

type BackupTarget struct {
	ID        string          `json:"id"`
	ServerID  string          `json:"server_id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	Retention json.RawMessage `json:"retention"`
	IsDefault bool            `json:"is_default"`
}

type Backup struct {
	ID          string          `json:"id"`
	ServerID    string          `json:"server_id"`
	TargetID    *string         `json:"target_id"`
	TriggeredBy *string         `json:"triggered_by"`
	Trigger     string          `json:"trigger"`
	Status      string          `json:"status"`
	SizeBytes   *int64          `json:"size_bytes"`
	SnapshotID  *string         `json:"snapshot_id"`
	Metadata    json.RawMessage `json:"metadata"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at"`
}

type ScheduledTask struct {
	ID        string          `json:"id"`
	ServerID  string          `json:"server_id"`
	Name      string          `json:"name"`
	CronExpr  string          `json:"cron_expr"`
	Action    string          `json:"action"`
	Payload   json.RawMessage `json:"payload"`
	Enabled   bool            `json:"enabled"`
	LastRun   *time.Time      `json:"last_run"`
	NextRun   *time.Time      `json:"next_run"`
	CreatedAt time.Time       `json:"created_at"`
}

type RefreshToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ── Scanner / Valuer helpers ─────────────────────────────────────

// strArray stores a []string as a JSON array in TEXT.
type strArray []string

func (a *strArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("strArray: cannot scan %T", src)
	}
	if len(b) == 0 {
		*a = nil
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a))
}

func (a strArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(a))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// jsonRaw stores a json.RawMessage as TEXT.
type jsonRaw json.RawMessage

func (j *jsonRaw) Scan(src any) error {
	if src == nil {
		*j = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		*j = append((*j)[:0], v...)
	case string:
		*j = []byte(v)
	default:
		return fmt.Errorf("jsonRaw: cannot scan %T", src)
	}
	return nil
}

func (j jsonRaw) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

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

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ── Refresh Tokens ───────────────────────────────────────────────

func (s *Store) CreateRefreshToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES (?, ?, ?, ?)`,
		uuid.NewString(), userID, tokenHash, expiresAt,
	)
	return err
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

// ── Nodes ────────────────────────────────────────────────────────

func (s *Store) CreateNode(ctx context.Context, n *Node, token string) (*Node, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes (id, name, fqdn, port, scheme, token, location)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, n.Name, n.FQDN, n.Port, n.Scheme, token, n.Location,
	)
	if err != nil {
		return nil, err
	}
	return s.GetNode(ctx, id)
}

func (s *Store) EnsureNode(ctx context.Context, name, fqdn string, port int, scheme, token string) (*Node, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM nodes WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return s.CreateNode(ctx, &Node{
			Name:   name,
			FQDN:   fqdn,
			Port:   port,
			Scheme: scheme,
		}, token)
	}
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE nodes SET fqdn=?, port=?, scheme=?, token=? WHERE id=?`,
		fqdn, port, scheme, token, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetNode(ctx, id)
}

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	var n Node
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, fqdn, port, scheme, token, memory_mb, disk_gb, cpu_cores, location, created_at, last_seen FROM nodes WHERE id = ?`,
		id,
	).Scan(&n.ID, &n.Name, &n.FQDN, &n.Port, &n.Scheme, &n.Token, &n.MemoryMb, &n.DiskGb, &n.CPUCores, &n.Location, &n.CreatedAt, &n.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("node not found")
	}
	return &n, err
}

func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, fqdn, port, scheme, token, memory_mb, disk_gb, cpu_cores, location, created_at, last_seen FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.FQDN, &n.Port, &n.Scheme, &n.Token, &n.MemoryMb, &n.DiskGb, &n.CPUCores, &n.Location, &n.CreatedAt, &n.LastSeen); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

func (s *Store) UpdateNode(ctx context.Context, id string, n *Node) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET name=?, fqdn=?, port=?, scheme=?, location=? WHERE id=?`,
		n.Name, n.FQDN, n.Port, n.Scheme, n.Location, id,
	)
	return err
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	n, err := s.CountServersForNode(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrNodeHasServers
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateNodeSeen(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_seen = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateNodeHeartbeat(ctx context.Context, id string, memoryMb, diskGb, cpuCores *int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET memory_mb=?, disk_gb=?, cpu_cores=?, last_seen=CURRENT_TIMESTAMP WHERE id=?`,
		memoryMb, diskGb, cpuCores, id,
	)
	return err
}

// ── Servers ──────────────────────────────────────────────────────

func (s *Store) CreateServer(ctx context.Context, srv *Server) (*Server, error) {
	id := uuid.NewString()
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, auto_start, tags, settings)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, srv.NodeID, srv.OwnerID, srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings),
	)
	if err != nil {
		return nil, err
	}
	return s.GetServer(ctx, id)
}

func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	var srv Server
	err := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers WHERE id = ?`, id,
	).Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
		&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
		&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server not found")
	}
	return &srv, err
}

func (s *Store) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, &srv)
	}
	return servers, rows.Err()
}

func (s *Store) CountServersForNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE node_id = ?`, nodeID).Scan(&n)
	return n, err
}

func (s *Store) ListServersForUser(ctx context.Context, userID string) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_id, name, description, platform, mc_version, loader_version,
		  directory_path, java_binary, jvm_args, port, ram_mb_min, ram_mb_max, status, auto_start, tags, settings, created_at, updated_at
		 FROM servers
		 WHERE owner_id = ?
		    OR id IN (
		        SELECT sp.server_id FROM server_permissions sp
		        WHERE sp.user_id = ? AND json_array_length(sp.permissions) > 0
		    )
		 ORDER BY name`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.NodeID, &srv.OwnerID, &srv.Name, &srv.Description, &srv.Platform, &srv.MCVersion, &srv.LoaderVersion,
			&srv.DirectoryPath, &srv.JavaBinary, (*strArray)(&srv.JVMArgs), &srv.Port, &srv.RAMMbMin, &srv.RAMMbMax,
			&srv.Status, &srv.AutoStart, (*strArray)(&srv.Tags), (*jsonRaw)(&srv.Settings), &srv.CreatedAt, &srv.UpdatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, &srv)
	}
	return servers, rows.Err()
}

func (s *Store) UserCanAccessServer(ctx context.Context, userID, serverID string) (bool, error) {
	return s.UserHasServerPermission(ctx, userID, serverID, ServerPermissionView)
}

func (s *Store) UserHasServerPermission(ctx context.Context, userID, serverID string, needed ServerPermission) (bool, error) {
	var ownerID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM servers WHERE id = ?`, serverID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ownerID == userID {
		return true, nil
	}
	perms, ok, err := s.GetServerPermissions(ctx, serverID, userID)
	if err != nil || !ok {
		return false, err
	}
	return HasServerPermission(perms, needed), nil
}

// UserHasServerGroupAccess reports whether a user can reach a group's
// read-level endpoints: true for the owner, and for any collaborator holding
// the group or any of its leaves.
func (s *Store) UserHasServerGroupAccess(ctx context.Context, userID, serverID string, group ServerPermission) (bool, error) {
	var ownerID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM servers WHERE id = ?`, serverID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ownerID == userID {
		return true, nil
	}
	perms, ok, err := s.GetServerPermissions(ctx, serverID, userID)
	if err != nil || !ok {
		return false, err
	}
	return HasServerGroupAccess(perms, group), nil
}

func (s *Store) ListServerMembers(ctx context.Context, serverID string) ([]*ServerMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sp.server_id, sp.user_id, u.email, u.display_name, u.role, sp.permissions
		 FROM server_permissions sp
		 JOIN users u ON u.id = sp.user_id
		 WHERE sp.server_id = ?
		 ORDER BY lower(u.email)`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*ServerMember
	for rows.Next() {
		var m ServerMember
		var perms strArray
		if err := rows.Scan(&m.ServerID, &m.UserID, &m.Email, &m.DisplayName, &m.Role, &perms); err != nil {
			return nil, err
		}
		normalized, err := NormalizeServerPermissions([]string(perms))
		if err != nil {
			return nil, err
		}
		m.Permissions = normalized
		members = append(members, &m)
	}
	return members, rows.Err()
}

func (s *Store) GetServerMember(ctx context.Context, serverID, userID string) (*ServerMember, error) {
	var m ServerMember
	var perms strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT sp.server_id, sp.user_id, u.email, u.display_name, u.role, sp.permissions
		 FROM server_permissions sp
		 JOIN users u ON u.id = sp.user_id
		 WHERE sp.server_id = ? AND sp.user_id = ?`,
		serverID, userID,
	).Scan(&m.ServerID, &m.UserID, &m.Email, &m.DisplayName, &m.Role, &perms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeServerPermissions([]string(perms))
	if err != nil {
		return nil, err
	}
	m.Permissions = normalized
	return &m, nil
}

func (s *Store) SetServerPermissions(ctx context.Context, serverID, userID string, perms []string) error {
	normalized, err := NormalizeServerPermissions(perms)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO server_permissions (server_id, user_id, permissions)
		 VALUES (?, ?, ?)
		 ON CONFLICT(server_id, user_id) DO UPDATE SET permissions=excluded.permissions`,
		serverID, userID, strArray(normalized),
	)
	return err
}

func (s *Store) SetServerPermissionsIfCurrent(ctx context.Context, serverID, userID string, perms, expected []string) error {
	normalized, err := NormalizeServerPermissions(perms)
	if err != nil {
		return err
	}
	expectedNormalized, err := NormalizeServerPermissions(expected)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current strArray
	err = tx.QueryRowContext(ctx,
		`SELECT permissions FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerMemberNotFound
	}
	if err != nil {
		return err
	}
	currentNormalized, err := NormalizeServerPermissions([]string(current))
	if err != nil {
		return err
	}
	if !samePermissionSet(currentNormalized, expectedNormalized) {
		return ErrServerPermissionsStale
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE server_permissions SET permissions = ? WHERE server_id = ? AND user_id = ?`,
		strArray(normalized), serverID, userID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteServerPermissions(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	)
	return err
}

func (s *Store) GetServerPermissions(ctx context.Context, serverID, userID string) ([]string, bool, error) {
	var perms strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT permissions FROM server_permissions WHERE server_id = ? AND user_id = ?`,
		serverID, userID,
	).Scan(&perms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	normalized, err := NormalizeServerPermissions([]string(perms))
	if err != nil {
		return nil, true, err
	}
	return normalized, true, nil
}

func (s *Store) UpdateServer(ctx context.Context, id string, srv *Server) error {
	if srv.Settings == nil {
		srv.Settings = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name=?, description=?, platform=?, mc_version=?, loader_version=?,
		  directory_path=?, java_binary=?, jvm_args=?, port=?, ram_mb_min=?, ram_mb_max=?,
		  auto_start=?, tags=?, settings=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		srv.Name, srv.Description, srv.Platform, srv.MCVersion, srv.LoaderVersion,
		srv.DirectoryPath, srv.JavaBinary, strArray(srv.JVMArgs), srv.Port, srv.RAMMbMin, srv.RAMMbMax,
		srv.AutoStart, strArray(srv.Tags), jsonRaw(srv.Settings), id,
	)
	return err
}

func (s *Store) UpdateServerStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id)
	return err
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	return err
}

// ── Installed Mods ───────────────────────────────────────────────

const modCols = `id, server_id, source, source_id, version_id, name, version, file_name, sha256, sha512, pinned, enabled, install_path, installed_as_dep, installed_at`

func scanMod(sc interface {
	Scan(...any) error
}, m *InstalledMod) error {
	return sc.Scan(&m.ID, &m.ServerID, &m.Source, &m.SourceID, &m.VersionID, &m.Name, &m.Version,
		&m.FileName, &m.SHA256, &m.SHA512, &m.Pinned, &m.Enabled, &m.InstallPath, &m.InstalledAsDep, &m.InstalledAt)
}

func (s *Store) ListMods(ctx context.Context, serverID string) ([]*InstalledMod, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+modCols+` FROM installed_mods WHERE server_id = ? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mods []*InstalledMod
	for rows.Next() {
		var m InstalledMod
		if err := scanMod(rows, &m); err != nil {
			return nil, err
		}
		mods = append(mods, &m)
	}
	return mods, rows.Err()
}

func (s *Store) CreateMod(ctx context.Context, m *InstalledMod) (*InstalledMod, error) {
	id := uuid.NewString()
	if m.InstallPath == "" {
		m.InstallPath = "/mods"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO installed_mods (id, server_id, source, source_id, version_id, name, version, file_name, sha256, sha512, pinned, install_path, installed_as_dep)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, m.ServerID, m.Source, m.SourceID, m.VersionID, m.Name, m.Version, m.FileName, m.SHA256, m.SHA512, m.Pinned, m.InstallPath, m.InstalledAsDep,
	)
	if err != nil {
		return nil, err
	}
	var out InstalledMod
	err = scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, id), &out)
	return &out, err
}

// StampModHash records the file's sha512 without otherwise changing the row. A
// stored hash marks the jar as "already hashed and looked up", so reconciliation
// won't rehash or re-query it on every Mods-tab load.
func (s *Store) StampModHash(ctx context.Context, id, sha512 string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET sha512=? WHERE id=?`, sha512, id)
	return err
}

// RecognizeMod upgrades an existing (typically "custom") row to a known source
// after its jar was matched to that source's file index by hash — promoting it to
// a managed mod with version metadata and update checking, while keeping the same
// on-disk file and enabled state.
func (s *Store) RecognizeMod(ctx context.Context, id, source, sourceID, versionID, name, version, sha512 string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE installed_mods SET source=?, source_id=?, version_id=?, name=?, version=?, sha512=? WHERE id=?`,
		source, sourceID, versionID, name, version, sha512, id,
	)
	return err
}

func (s *Store) GetMod(ctx context.Context, id string) (*InstalledMod, error) {
	var m InstalledMod
	err := scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, id), &m)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("mod not found")
	}
	return &m, err
}

// UpdateMod swaps the version/file metadata of an existing mod row (used by the
// update flow after a new jar is pushed to the agent).
func (s *Store) UpdateMod(ctx context.Context, m *InstalledMod) (*InstalledMod, error) {
	_, err := s.db.ExecContext(ctx,
		`UPDATE installed_mods SET version_id=?, name=?, version=?, file_name=?, sha256=? WHERE id=?`,
		m.VersionID, m.Name, m.Version, m.FileName, m.SHA256, m.ID,
	)
	if err != nil {
		return nil, err
	}
	var out InstalledMod
	err = scanMod(s.db.QueryRowContext(ctx, `SELECT `+modCols+` FROM installed_mods WHERE id = ?`, m.ID), &out)
	return &out, err
}

// SetModPinned toggles whether a mod is excluded from bulk updates.
func (s *Store) SetModPinned(ctx context.Context, id string, pinned bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET pinned=? WHERE id=?`, pinned, id)
	return err
}

// SetModEnabled records the enabled state and the resulting on-disk file name
// (jars are renamed to "<name>.disabled" when disabled, so file_name must follow
// so uninstall/update keep targeting the real file).
func (s *Store) SetModEnabled(ctx context.Context, id string, enabled bool, fileName string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE installed_mods SET enabled=?, file_name=? WHERE id=?`, enabled, fileName, id)
	return err
}

func (s *Store) DeleteMod(ctx context.Context, id string) (*InstalledMod, error) {
	var m InstalledMod
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, file_name FROM installed_mods WHERE id = ?`, id,
	).Scan(&m.ID, &m.ServerID, &m.FileName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("mod not found")
	}
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM installed_mods WHERE id = ?`, id)
	return &m, err
}

// ── Mod dependency graph ─────────────────────────────────────────

// AddModDependency records that dependentPID requires dependencyPID on a server.
// Idempotent: re-recording the same edge is a no-op.
func (s *Store) AddModDependency(ctx context.Context, serverID, dependentPID, dependencyPID string) error {
	if dependentPID == "" || dependencyPID == "" || dependentPID == dependencyPID {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO mod_dependencies (server_id, dependent_project_id, dependency_project_id)
		 VALUES (?,?,?)`,
		serverID, dependentPID, dependencyPID,
	)
	return err
}

// ListModDependencies returns every dependency edge for a server.
func (s *Store) ListModDependencies(ctx context.Context, serverID string) ([]ModDependency, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dependent_project_id, dependency_project_id FROM mod_dependencies WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []ModDependency
	for rows.Next() {
		var e ModDependency
		if err := rows.Scan(&e.DependentProjectID, &e.DependencyProjectID); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// DeleteModDependencyEdges removes every edge touching a project id (as either
// side) on a server — used on uninstall so the removed mod stops counting as a
// dependent and any now-unreferenced deps become orphaned.
func (s *Store) DeleteModDependencyEdges(ctx context.Context, serverID, projectID string) error {
	if projectID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM mod_dependencies
		 WHERE server_id = ? AND (dependent_project_id = ? OR dependency_project_id = ?)`,
		serverID, projectID, projectID,
	)
	return err
}

// ── Backup Targets ───────────────────────────────────────────────

func (s *Store) ListBackupTargets(ctx context.Context, serverID string) ([]*BackupTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, type, config, retention, is_default FROM backup_targets WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []*BackupTarget
	for rows.Next() {
		var t BackupTarget
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Type, (*jsonRaw)(&t.Config), (*jsonRaw)(&t.Retention), &t.IsDefault); err != nil {
			return nil, err
		}
		targets = append(targets, &t)
	}
	return targets, rows.Err()
}

func (s *Store) CreateBackupTarget(ctx context.Context, t *BackupTarget) (*BackupTarget, error) {
	id := uuid.NewString()
	if t.Config == nil {
		t.Config = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_targets (id, server_id, name, type, config, retention, is_default)
		 VALUES (?,?,?,?,?,?,?)`,
		id, t.ServerID, t.Name, t.Type, jsonRaw(t.Config), jsonRaw(t.Retention), t.IsDefault,
	)
	if err != nil {
		return nil, err
	}
	var out BackupTarget
	err = s.db.QueryRowContext(ctx,
		`SELECT id, server_id, name, type, config, retention, is_default FROM backup_targets WHERE id = ?`, id,
	).Scan(&out.ID, &out.ServerID, &out.Name, &out.Type, (*jsonRaw)(&out.Config), (*jsonRaw)(&out.Retention), &out.IsDefault)
	return &out, err
}

// ── Backups ──────────────────────────────────────────────────────

func (s *Store) ListBackups(ctx context.Context, serverID string) ([]*Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE server_id=? ORDER BY started_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var backups []*Backup
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.ServerID, &b.TargetID, &b.TriggeredBy, &b.Trigger, &b.Status, &b.SizeBytes, &b.SnapshotID, (*jsonRaw)(&b.Metadata), &b.StartedAt, &b.CompletedAt); err != nil {
			return nil, err
		}
		backups = append(backups, &b)
	}
	return backups, rows.Err()
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, id)
	return err
}

func (s *Store) GetBackup(ctx context.Context, id string) (*Backup, error) {
	var b Backup
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE id=?`, id,
	).Scan(&b.ID, &b.ServerID, &b.TargetID, &b.TriggeredBy, &b.Trigger, &b.Status, &b.SizeBytes, &b.SnapshotID, (*jsonRaw)(&b.Metadata), &b.StartedAt, &b.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("backup not found")
	}
	return &b, err
}

// UpdateBackupResult marks a running backup as success/failed. Pass nil
// sizeBytes to leave the column unchanged (e.g. on failure).
func (s *Store) UpdateBackupResult(ctx context.Context, id, status string, sizeBytes *int64, errMsg string) error {
	if sizeBytes != nil {
		_, err := s.db.ExecContext(ctx,
			`UPDATE backups SET status=?, size_bytes=?, completed_at=CURRENT_TIMESTAMP WHERE id=?`,
			status, *sizeBytes, id)
		return err
	}
	// On failure, store the error message in metadata for visibility
	meta, _ := json.Marshal(map[string]string{"error": errMsg})
	_, err := s.db.ExecContext(ctx,
		`UPDATE backups SET status=?, completed_at=CURRENT_TIMESTAMP, metadata=? WHERE id=?`,
		status, string(meta), id)
	return err
}

func (s *Store) CreateBackup(ctx context.Context, b *Backup) (*Backup, error) {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backups (id, server_id, target_id, triggered_by, trigger, status)
		 VALUES (?,?,?,?,?,?)`,
		id, b.ServerID, b.TargetID, b.TriggeredBy, b.Trigger, b.Status,
	)
	if err != nil {
		return nil, err
	}
	var out Backup
	err = s.db.QueryRowContext(ctx,
		`SELECT id, server_id, target_id, triggered_by, trigger, status, size_bytes, snapshot_id, metadata, started_at, completed_at
		 FROM backups WHERE id = ?`, id,
	).Scan(&out.ID, &out.ServerID, &out.TargetID, &out.TriggeredBy, &out.Trigger, &out.Status, &out.SizeBytes, &out.SnapshotID, (*jsonRaw)(&out.Metadata), &out.StartedAt, &out.CompletedAt)
	return &out, err
}

// ── Scheduled Tasks ──────────────────────────────────────────────

func (s *Store) ListTasks(ctx context.Context, serverID string) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at
		 FROM scheduled_tasks WHERE server_id=? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// ListAllEnabledTasks returns every enabled task across all servers. The
// scheduler uses this to (re)register cron entries.
func (s *Store) ListAllEnabledTasks(ctx context.Context) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at
		 FROM scheduled_tasks WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// UpdateTaskLastRun records when a task fired and the next scheduled time.
func (s *Store) UpdateTaskLastRun(ctx context.Context, id string, lastRun time.Time, nextRun *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET last_run=?, next_run=? WHERE id=?`,
		lastRun, nextRun, id)
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*ScheduledTask, error) {
	var t ScheduledTask
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, name, cron_expr, action, payload, enabled, last_run, next_run, created_at
		 FROM scheduled_tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.ServerID, &t.Name, &t.CronExpr, &t.Action, (*jsonRaw)(&t.Payload), &t.Enabled, &t.LastRun, &t.NextRun, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found")
	}
	return &t, err
}

func (s *Store) CreateTask(ctx context.Context, t *ScheduledTask) (*ScheduledTask, error) {
	id := uuid.NewString()
	// Populate next_run up front so the UI shows an accurate countdown before the
	// task has ever fired. Disabled tasks have no upcoming run.
	var next *time.Time
	if t.Enabled {
		next = NextRunForCron(t.CronExpr, time.Now())
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (id, server_id, name, cron_expr, action, payload, enabled, next_run)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, t.ServerID, t.Name, t.CronExpr, t.Action, jsonRaw(t.Payload), t.Enabled, next,
	)
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) UpdateTask(ctx context.Context, id string, t *ScheduledTask) error {
	// Recompute next_run: the cron expr or enabled flag may have changed. Clears
	// it (NULL) when the task is disabled.
	var next *time.Time
	if t.Enabled {
		next = NextRunForCron(t.CronExpr, time.Now())
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET name=?, cron_expr=?, action=?, payload=?, enabled=?, next_run=? WHERE id=?`,
		t.Name, t.CronExpr, t.Action, jsonRaw(t.Payload), t.Enabled, next, id,
	)
	return err
}

// SetTaskNextRun updates only the cached next_run, used by the scheduler to keep
// the stored countdown fresh (e.g. after an API restart left it stale/past).
func (s *Store) SetTaskNextRun(ctx context.Context, id string, next *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET next_run=? WHERE id=?`, next, id)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id=?`, id)
	return err
}

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
