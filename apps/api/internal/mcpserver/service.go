// Package mcpserver is ServerManager's Model Context Protocol tool facade.
//
// It is a facade, not a proxy, and that distinction is the security boundary.
// Each tool is a named operation with a typed, validated argument set. There is
// no tool that takes a URL, an API path, a shell command, SQL, a filesystem
// path, an environment variable, a node id, or a raw Minecraft console command,
// so a model cannot express a request outside the set below however it is
// steered.
//
// run_console_command is the case worth being precise about, because its name
// suggests otherwise. It does not accept a command. It accepts a verb from the
// closed table in console.go plus arguments that the verb's own builder parses,
// and application code renders the line that reaches the server. A command
// string never crosses this boundary in the inbound direction, which is what
// keeps `op`, `stop`, `execute`, and every mod-added command unreachable — a
// property that matters precisely because this same facade hands the model
// untrusted log and mod text as evidence.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// Principal is the authority behind one MCP request: the delegation, its
// bounds, and the human it acts as. It is built by the transport's token
// verifier and closed over by the per-request tool handlers, so a handler can
// never see another request's authority.
//
// Scopes and ServerIDs are bounds, not grants. Nothing here authorizes anything
// on its own; authorize() intersects them with the owner's live RBAC.
type Principal struct {
	UserID     string
	GrantID    string
	ClientName string
	IP         string
	Scopes     []string
	ServerIDs  []string
}

// HasScope reports whether the token carries a capability.
func (p *Principal) HasScope(s store.MCPScope) bool {
	if p == nil {
		return false
	}
	return store.HasMCPScope(p.Scopes, s)
}

// NodeClient is the slice of the agent API this facade is allowed to reach. It
// is an interface rather than *agent.Client so the set is enumerable in one
// place: there is no method here for console commands, kill, reinstall,
// restore, file access, or anything else the facade must not expose, and adding
// one is a visible edit rather than an accident of embedding.
type NodeClient interface {
	GetServerStats(ctx context.Context, serverID string) (*agent.ServerStats, error)
	StartServer(ctx context.Context, serverID string, cfg map[string]any) error
	StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error
	RestartServer(ctx context.Context, serverID string, cfg map[string]any) error
}

// NodeResolver produces a client for the node a server lives on. Injectable so
// tests can drive the whole facade without a running agent.
type NodeResolver func(ctx context.Context, srv *store.Server) (NodeClient, error)

// OperatorActor is the audit identity passed to the bounded application
// operation adapter. It carries attribution, never authentication authority.
type OperatorActor struct {
	UserID     string
	GrantID    string
	ClientName string
	IP         string
}

// OperatorBackend reuses the existing mod and backup application operations
// without making the MCP facade depend on HTTP handlers (which themselves use
// MCP action execution and would create an import cycle).
type OperatorBackend interface {
	Invoke(ctx context.Context, actor OperatorActor, operation, serverID, modID string, args map[string]any) (any, error)
}

type MigrationStarter interface {
	Trigger(ctx context.Context, serverID, targetMC string) (*store.VersionMigration, error)
}

// ActionNotifier delivers an approval prompt to the one person who can answer
// it. Narrow on purpose: the facade should be able to raise a prompt without
// being able to reach the rest of the notification system.
type ActionNotifier interface {
	EmitToUser(userID string, evt notify.Event)
}

type Option func(*Service)

func WithOperatorBackend(backend OperatorBackend) Option {
	return func(svc *Service) {
		svc.operator = backend
	}
}

func WithMigrationStarter(starter MigrationStarter) Option {
	return func(svc *Service) { svc.migrations = starter }
}

// WithNotifier wires the approval prompt. Optional: a Service without one still
// files and executes requests exactly as before, it just does not raise a
// dashboard alert — the same nil-tolerant shape Engine.Emit already has.
func WithNotifier(n ActionNotifier) Option {
	return func(svc *Service) { svc.notifier = n }
}

// Service builds per-request MCP servers over the panel's store.
type Service struct {
	store      *store.Store
	nodes      NodeResolver
	operator   OperatorBackend
	migrations MigrationStarter
	notifier   ActionNotifier
	// now is injectable so tests can pin expiry boundaries.
	now func() time.Time

	// waits counts the await_action_request calls each delegation currently
	// holds open. See beginWait.
	waitsMu sync.Mutex
	waits   map[string]int
}

func New(s *store.Store, opts ...Option) *Service {
	svc := &Service{store: s, nodes: storeNodeResolver(s), now: time.Now, waits: map[string]int{}}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// maxConcurrentWaits bounds how many await_action_request calls one delegation
// may hold open at once.
//
// A wait is the only tool call that deliberately does nothing for two and a
// half minutes, and the transport is stateless JSON, so each one is a held
// connection, a goroutine, and a database read every two seconds for its whole
// life. The rate limiter counts calls arriving, not calls still running, so
// without this a single grant could stack them until it owned a meaningful
// share of the process.
//
// Four is well above real use: an agent waits on the request it just filed,
// and the tool tells it not to file a second.
const maxConcurrentWaits = 4

// beginWait claims one of a delegation's concurrent-wait slots, reporting
// whether it got one. endWait must be deferred immediately after a true.
func (svc *Service) beginWait(grantID string) bool {
	svc.waitsMu.Lock()
	defer svc.waitsMu.Unlock()
	if svc.waits[grantID] >= maxConcurrentWaits {
		return false
	}
	svc.waits[grantID]++
	return true
}

// endWait releases a slot, and drops the key entirely when the last wait under
// it finishes so an idle panel does not accumulate a row per grant it ever saw.
func (svc *Service) endWait(grantID string) {
	svc.waitsMu.Lock()
	defer svc.waitsMu.Unlock()
	if svc.waits[grantID] <= 1 {
		delete(svc.waits, grantID)
		return
	}
	svc.waits[grantID]--
}

// storeNodeResolver is the production resolver: look the node up and dial it
// with its own token. The token never leaves this function.
func storeNodeResolver(s *store.Store) NodeResolver {
	return func(ctx context.Context, srv *store.Server) (NodeClient, error) {
		node, err := s.GetNode(ctx, srv.NodeID)
		if err != nil {
			return nil, err
		}
		return agent.New(node.Scheme, node.FQDN, node.Port, node.Token), nil
	}
}

// errForbidden is the single answer for every authorization failure. A model
// that is told "not in your allowlist" versus "you lack that permission" learns
// the shape of the fleet; one that is told "not permitted" learns only that it
// should stop.
var errForbidden = errors.New("not permitted: this grant does not allow that operation on that server")

var errUnknownServer = errors.New("unknown server, or this grant does not cover it")

// authorize is the whole authorization model in one place. Every tool calls it
// before touching anything, and the order matters:
//
//  1. reload the grant       — revocation and expiry land on this call
//  2. token scope            — the capability the human ticked
//  3. server allowlist       — the servers the human selected
//  4. reload the owner       — deletion and demotion land on this call
//  5. live per-server RBAC   — membership and leaf-permission changes land here
//
// Steps 1–3 are the grant's own bounds and have no bypass of any kind. Only
// step 5 honors the global admin role, exactly as the HTTP route gates do —
// which is what keeps an admin-owned grant bounded to the servers and
// capabilities it was actually given, while still letting an admin delegate on
// a server they administer rather than own.
//
// Nothing here is cached. A cache would buy a little latency in exchange for a
// window in which a revoked grant still works, and immediate revocation is the
// property this whole feature is built on.
func (svc *Service) authorize(ctx context.Context, p *Principal, serverID string, scope store.MCPScope, permission store.ServerPermission, group bool) (*store.Server, error) {
	if p == nil || serverID == "" {
		return nil, errForbidden
	}

	grant, err := svc.store.GetMCPGrantForUser(ctx, p.GrantID, p.UserID)
	if err != nil || !grant.Active(svc.now()) {
		return nil, errForbidden
	}
	if !p.HasScope(scope) || !store.HasMCPScope(grant.Scopes, scope) {
		return nil, errForbidden
	}
	if !grant.AllowsServer(serverID) {
		return nil, errForbidden
	}

	user, err := svc.store.GetUserByID(ctx, p.UserID)
	if err != nil || user == nil {
		return nil, errForbidden
	}

	if user.Role != "admin" {
		var ok bool
		if group {
			ok, err = svc.store.UserHasServerGroupAccess(ctx, p.UserID, serverID, permission)
		} else {
			ok, err = svc.store.UserHasServerPermission(ctx, p.UserID, serverID, permission)
		}
		if err != nil {
			slog.Error("mcp authorization check failed", "grant_id", p.GrantID, "server_id", serverID, "error", err)
			return nil, errForbidden
		}
		if !ok {
			return nil, errForbidden
		}
	}

	srv, err := svc.store.GetServer(ctx, serverID)
	if err != nil {
		// The allowlist named a server that no longer exists. Same answer as
		// "not yours": a deleted server is not something to disclose.
		return nil, errUnknownServer
	}
	return srv, nil
}

// Server builds the MCP server for one authenticated request.
//
// Tools are registered only when the grant carries their capability. That is
// defence in depth for a confused model, not the boundary: every handler
// re-checks authorization itself, because a tool list is ergonomics and
// authorize() is the actual gate.
func (svc *Service) Server(p *Principal) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:        ServerName,
		Title:       ServerTitle,
		Version:     ServerVersion,
		Description: "Diagnose and operate selected Minecraft servers within an explicit OAuth grant.",
	}, &mcp.ServerOptions{
		Instructions: Instructions,
	})
	svc.registerTools(s, p)
	return s
}

// toolError reports a failure to the model as a tool error rather than a
// protocol error, so it can read and adapt. The detail stays generic; the real
// cause is logged server-side.
func toolError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
