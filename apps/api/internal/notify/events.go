// Package notify turns the events the panel already detects (crashes, status
// flips, conflicts, backup/update outcomes, node heartbeat loss) into per-user
// alerts delivered through an in-app live feed, browser Web Push, and outbound
// webhooks. Detection points call Engine.Emit; everything else — recipient
// resolution, permission re-checks, de-duplication, durable delivery with retry
// — happens here.
package notify

// Severity ranks how urgent an alert is. Subscriptions set a minimum severity;
// an event below a user's threshold for that type is dropped for them.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	default:
		return 1 // info / unknown
	}
}

// Scope says what an event is about, which determines how a subscription is
// matched and which permission (if any) gates it.
type Scope string

const (
	ScopeServer Scope = "server" // gated by the user's view permission on the server
	ScopeNode   Scope = "node"   // node-level; visible to all authenticated users
	ScopeGlobal Scope = "global"
)

// Event type identifiers. Keep these stable — they are persisted in
// subscriptions and notifications rows.
const (
	EventServerCrash        = "server.crash"
	EventServerOffline      = "server.offline"
	EventServerOnline       = "server.online"
	EventServerStartFailed  = "server.start_failed"
	EventModConflict        = "mod.conflict"
	EventModUpdateAvailable = "mod.update_available"
	EventModUpdateApplied   = "mod.update_applied"
	EventModUpdateFailed    = "mod.update_failed"
	EventBackupSuccess      = "backup.success"
	EventBackupFailed       = "backup.failed"
	EventNodeOffline        = "node.offline"
	EventNodeOnline         = "node.online"
)

// EventDef is the static description of an event type, surfaced to the frontend
// so the subscription UI renders from one source of truth (mirrors the
// knownIntegrations allowlist pattern in the settings handler).
type EventDef struct {
	Type            string `json:"type"`
	Label           string `json:"label"`
	Description     string `json:"description"`
	DefaultSeverity string `json:"default_severity"`
	Scope           Scope  `json:"scope"`
}

// Catalog is the ordered, authoritative list of alertable events.
var Catalog = []EventDef{
	{EventServerCrash, "Server crashed", "A running server went offline unexpectedly (no panel-initiated stop).", SeverityCritical, ScopeServer},
	{EventServerOffline, "Server stopped", "A server transitioned to offline.", SeverityWarning, ScopeServer},
	{EventServerOnline, "Server online", "A server came online.", SeverityInfo, ScopeServer},
	{EventModConflict, "Mod conflict detected", "A mod incompatibility or crash-on-load was detected.", SeverityWarning, ScopeServer},
	{EventModUpdateApplied, "Mod update applied", "An auto-update run installed new mod versions.", SeverityInfo, ScopeServer},
	{EventModUpdateFailed, "Mod update failed", "An auto-update run failed.", SeverityWarning, ScopeServer},
	{EventBackupSuccess, "Backup succeeded", "A backup completed successfully.", SeverityInfo, ScopeServer},
	{EventBackupFailed, "Backup failed", "A backup did not complete.", SeverityWarning, ScopeServer},
	{EventNodeOffline, "Node offline", "An agent node stopped responding to heartbeats.", SeverityWarning, ScopeNode},
	{EventNodeOnline, "Node online", "An agent node started responding again.", SeverityInfo, ScopeNode},
}

// DefByType returns the catalog definition for an event type.
func DefByType(t string) (EventDef, bool) { return defByType(t) }

func defByType(t string) (EventDef, bool) {
	for _, d := range Catalog {
		if d.Type == t {
			return d, true
		}
	}
	return EventDef{}, false
}

// Event is a single occurrence handed to Engine.Emit. Severity and DedupeKey are
// optional; the engine fills sensible defaults from the catalog and scope.
type Event struct {
	Type       string
	Severity   string
	ServerID   string
	ServerName string
	NodeID     string
	NodeName   string
	Title      string
	Body       string
	Data       map[string]any
	DedupeKey  string
}
