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
	EventNodeDiskLow        = "node.disk_low"
	EventServerPerformance  = "server.performance"
	EventPlayerJoinDenied   = "player.join_denied"
	EventMCPActionRequested = "mcp.action_requested"
	EventMCPActionResolved  = "mcp.action_resolved"
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
	// DefaultOn subscribes new users to this event's in-app alerts without them
	// opting in. Reserved for events that are useless when missed: a player
	// waiting at the door gives up long before an operator thinks to go looking
	// for a notification setting they never knew existed.
	DefaultOn bool `json:"default_on,omitempty"`
}

// Catalog is the ordered, authoritative list of alertable events.
var Catalog = []EventDef{
	{EventServerCrash, "Server crashed", "A running server went offline unexpectedly (no panel-initiated stop).", SeverityCritical, ScopeServer, false},
	{EventServerOffline, "Server stopped", "A server transitioned to offline.", SeverityWarning, ScopeServer, false},
	{EventServerOnline, "Server online", "A server came online.", SeverityInfo, ScopeServer, false},
	{EventPlayerJoinDenied, "Player turned away by the whitelist", "Someone who is not on the whitelist tried to join. The alert offers to let them in.", SeverityWarning, ScopeServer, true},
	{EventMCPActionRequested, "AI agent asked to act on a server", "A connected agent filed a start, stop, restart, or version-upgrade request. The alert offers to approve or deny it.", SeverityWarning, ScopeServer, true},
	{EventMCPActionResolved, "Agent action request settled", "An agent's request was approved, denied, or ran automatically under your approval policy.", SeverityInfo, ScopeServer, true},
	{EventModConflict, "Mod conflict detected", "A mod incompatibility or crash-on-load was detected.", SeverityWarning, ScopeServer, false},
	{EventModUpdateApplied, "Mod update applied", "An auto-update run installed new mod versions.", SeverityInfo, ScopeServer, false},
	{EventModUpdateFailed, "Mod update failed", "An auto-update run failed.", SeverityWarning, ScopeServer, false},
	{EventBackupSuccess, "Backup succeeded", "A backup completed successfully.", SeverityInfo, ScopeServer, false},
	{EventBackupFailed, "Backup failed", "A backup did not complete.", SeverityWarning, ScopeServer, false},
	{EventServerPerformance, "Server under sustained load", "CPU or memory stayed above the alert threshold for several consecutive samples.", SeverityWarning, ScopeServer, false},
	{EventNodeOffline, "Node offline", "An agent node stopped responding to heartbeats.", SeverityWarning, ScopeNode, false},
	{EventNodeOnline, "Node online", "An agent node started responding again.", SeverityInfo, ScopeNode, false},
	{EventNodeDiskLow, "Node disk low", "A backup left the node's disk nearly full.", SeverityWarning, ScopeNode, false},
}

// DefaultOnTypes lists the events a new user is subscribed to automatically.
func DefaultOnTypes() []string {
	var out []string
	for _, d := range Catalog {
		if d.DefaultOn {
			out = append(out, d.Type)
		}
	}
	return out
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
