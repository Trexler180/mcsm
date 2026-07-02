package store

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

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
	ServerPermissionPlayersDelete    ServerPermission = "players.delete"

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
		string(ServerPermissionPlayersDelete),
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
	// CreatedBy is the user who created the task; the scheduler re-checks that
	// this user still holds the permission the action requires before each run.
	// Nil for legacy tasks created before attribution existed.
	CreatedBy *string `json:"created_by"`
}

// RequiredTaskPermission maps a scheduled-task action to the per-server
// permission a user must hold to create or run it, so a task can never do more
// than its creator could do by hand. The bool reports whether the action is
// recognized at all.
func RequiredTaskPermission(action string) (ServerPermission, bool) {
	switch action {
	case "command":
		return ServerPermissionConsole, true
	case "restart":
		return ServerPermissionPowerRestart, true
	case "stop":
		return ServerPermissionPowerStop, true
	case "backup":
		return ServerPermissionBackupsCreate, true
	case "mod_update":
		return ServerPermissionModsUpdate, true
	default:
		return "", false
	}
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
