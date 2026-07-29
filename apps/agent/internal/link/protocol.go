// Package link implements the agent side of the MCSM helper mod link.
//
// The wire contract is specified in apps/mod/PROTOCOL.md and pinned by the
// golden fixtures in apps/mod/fixtures, which both this package's tests and the
// mod's Java tests parse. A field added on one side but not the other fails that
// side's test — that is the whole mechanism keeping two hand-written
// implementations honest.
package link

import "encoding/json"

// ProtocolVersion is the only version this agent speaks. A mod announcing
// anything else is closed with CloseBadVersion rather than served badly.
const ProtocolVersion = 1

// Frame types.
const (
	TypeHello       = "hello"
	TypeWelcome     = "welcome"
	TypeSnapshot    = "snapshot"
	TypeEvent       = "event"
	TypeRPCRequest  = "rpc_request"
	TypeRPCResponse = "rpc_response"
)

// Event kinds.
const (
	EventPlayerJoin     = "player_join"
	EventPlayerLeave    = "player_leave"
	EventPlayerDeath    = "player_death"
	EventServerReady    = "server_ready"
	EventServerStopping = "server_stopping"
)

// RPC error codes.
const (
	ErrUnknownMethod  = "unknown_method"
	ErrInvalidParams  = "invalid_params"
	ErrPlayerNotFound = "player_not_found"
	ErrTimeout        = "timeout"
	ErrUnsupported    = "unsupported"
	ErrInternal       = "internal"
)

// WebSocket close codes carrying protocol meaning. Codes in the 4xxx range are
// application-defined; these tell the mod whether reconnecting can ever help.
const (
	CloseBadVersion   = 4400
	CloseUnauthorized = 4401
	CloseDuplicate    = 4409
)

// Envelope is the outer shape of every frame (PROTOCOL.md §2).
//
// Data stays raw so a frame can be routed on Type before its payload is
// interpreted, and so an unknown type costs nothing to skip.
type Envelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	TS   int64           `json:"ts"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Hello is the mod's opening frame.
type Hello struct {
	ModVersion    string   `json:"mod_version"`
	MCVersion     string   `json:"mc_version"`
	Loader        string   `json:"loader"`
	LoaderVersion string   `json:"loader_version"`
	ServerBrand   string   `json:"server_brand"`
	Capabilities  []string `json:"capabilities"`
}

// HasCapability reports whether the connected mod advertises a capability.
// Capability checks are how the agent stays compatible with a Paper adapter
// that cannot report everything the Fabric build can.
func (h Hello) HasCapability(name string) bool {
	for _, c := range h.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// Welcome is the agent's reply accepting a session.
type Welcome struct {
	HeartbeatMS          int64    `json:"heartbeat_ms"`
	AcceptedCapabilities []string `json:"accepted_capabilities"`
	MaxFrameBytes        int64    `json:"max_frame_bytes"`
}

// TPS holds rolling tick-rate averages.
type TPS struct {
	M1  float64 `json:"m1"`
	M5  float64 `json:"m5"`
	M15 float64 `json:"m15"`
}

// MSPT holds tick-duration statistics in milliseconds.
type MSPT struct {
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// Heap holds JVM heap usage in megabytes.
type Heap struct {
	UsedMB      int64 `json:"used_mb"`
	CommittedMB int64 `json:"committed_mb"`
	MaxMB       int64 `json:"max_mb"`
}

// GC holds cumulative garbage-collection counters.
type GC struct {
	Collections int64 `json:"collections"`
	TimeMS      int64 `json:"time_ms"`
}

// Chunks holds loaded-chunk totals.
type Chunks struct {
	Loaded int64 `json:"loaded"`
}

// Entities holds entity totals.
type Entities struct {
	Total int64 `json:"total"`
}

// Dimension is a per-world breakdown.
type Dimension struct {
	ID       string `json:"id"`
	Chunks   int64  `json:"chunks"`
	Entities int64  `json:"entities"`
}

// PlayerInfo describes one online player.
type PlayerInfo struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	PingMS    int    `json:"ping_ms"`
	Dimension string `json:"dimension"`
}

// Players is the authoritative online-player set.
type Players struct {
	Online int          `json:"online"`
	Max    int          `json:"max"`
	List   []PlayerInfo `json:"list"`
}

// Snapshot is the full state frame. It is the reconcile anchor: the agent trusts
// it over its own event-derived view, which is what makes dropping an event
// under backpressure safe.
type Snapshot struct {
	UptimeMS   int64       `json:"uptime_ms"`
	TPS        TPS         `json:"tps"`
	MSPT       MSPT        `json:"mspt"`
	Heap       Heap        `json:"heap"`
	GC         GC          `json:"gc"`
	Chunks     Chunks      `json:"chunks"`
	Entities   Entities    `json:"entities"`
	Dimensions []Dimension `json:"dimensions"`
	Players    Players     `json:"players"`
}

// Event is a best-effort push notification. Never derive durable state from one.
type Event struct {
	Kind    string `json:"kind"`
	UUID    string `json:"uuid,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
}

// RPCRequest is an agent-to-mod call.
type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// RPCError describes a failed call.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RPCResponse is the mod's reply. Exactly one is sent per request.
type RPCResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// ExecResult is the payload of a successful command.exec call — the captured
// output that writing to stdin could never give us.
type ExecResult struct {
	Output  []string `json:"output"`
	Success bool     `json:"success"`
}

// RPC method names (PROTOCOL.md §4).
const (
	MethodPlayerKick      = "player.kick"
	MethodPlayerBan       = "player.ban"
	MethodPlayerPardon    = "player.pardon"
	MethodWhitelistAdd    = "whitelist.add"
	MethodWhitelistRemove = "whitelist.remove"
	MethodWhitelistList   = "whitelist.list"
	MethodOpGrant         = "op.grant"
	MethodOpRevoke        = "op.revoke"
	MethodBroadcast       = "message.broadcast"
	MethodMessagePlayer   = "message.player"
	MethodServerSave      = "server.save"
	MethodServerStop      = "server.stop"
	MethodCommandExec     = "command.exec"
)

// Capabilities a mod may advertise.
const (
	CapVitals       = "vitals"
	CapPlayerEvents = "player_events"
	CapRPC          = "rpc"
	CapCommandExec  = "command_exec"
)
