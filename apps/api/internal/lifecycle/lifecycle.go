// Package lifecycle is the one implementation of a server's power actions —
// start, stop, restart, kill, reinstall, delete — shared by the HTTP API, the
// MCP facade and the scheduler. Each used to carry its own copy, and the
// copies drifted: one forgot the helper-mod flag, one never audited, one
// stopped without checking whether a safe update was mid-boot.
//
// Every action here respects the per-server operation lock, records who asked
// in the audit log, writes the server's status, and announces it on the event
// bus. Long actions (start, reinstall) run as operations the caller can follow.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// Agent is the slice of the node client lifecycle needs.
type Agent interface {
	RegisterDir(ctx context.Context, serverID, directory string) error
	StartServer(ctx context.Context, serverID string, cfg map[string]any) error
	StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error
	RestartServer(ctx context.Context, serverID string, cfg map[string]any) error
	KillServer(ctx context.Context, serverID string) error
	GetStatus(ctx context.Context, serverID string) (map[string]any, error)
	Reinstall(ctx context.Context, serverID string, cfg map[string]any) error
	PurgeServer(ctx context.Context, serverID, directory string, files, backups bool) error
}

// AgentResolver returns a client for the node a server lives on.
type AgentResolver func(ctx context.Context, srv *store.Server) (Agent, error)

// Service performs power actions.
type Service struct {
	store  *store.Store
	ops    *operations.Manager
	bus    *events.Bus
	agents AgentResolver
}

// New builds the service. agents may be nil to resolve real agents from the
// node table.
func New(s *store.Store, ops *operations.Manager, bus *events.Bus, agents AgentResolver) *Service {
	if agents == nil {
		agents = NodeAgents(s)
	}
	return &Service{store: s, ops: ops, bus: bus, agents: agents}
}

// NodeAgents resolves agents from the node table.
func NodeAgents(s *store.Store) AgentResolver {
	return func(ctx context.Context, srv *store.Server) (Agent, error) {
		node, err := s.GetNode(ctx, srv.NodeID)
		if err != nil {
			return nil, fmt.Errorf("node not found")
		}
		return agent.New(node.Scheme, node.FQDN, node.Port, node.Token), nil
	}
}

// Operations exposes the operation manager, for callers that guard their own
// short actions.
func (s *Service) Operations() *operations.Manager { return s.ops }

// ErrNodeUnreachable wraps a failure to reach a server's node.
var ErrNodeUnreachable = errors.New("the server's node could not be reached")

// PortInUseError refuses a start because another server on the same node is
// running on the same port. Servers may share a port; only one can run at a time.
type PortInUseError struct {
	Port  int
	Other string // the running server's name
}

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("%s is running on port %d; stop it first, or give this server another port", e.Other, e.Port)
}

// runningStatuses hold a server's port.
var runningStatuses = map[string]bool{"online": true, "starting": true, "stopping": true}

// portClash reports another server on srv's node that is running on srv's port.
func (s *Service) portClash(ctx context.Context, srv *store.Server) error {
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, other := range servers {
		if other.ID != srv.ID && other.NodeID == srv.NodeID && other.Port == srv.Port && runningStatuses[other.Status] {
			return &PortInUseError{Port: srv.Port, Other: other.Name}
		}
	}
	return nil
}

// SetStatus records a server's status and announces it.
func (s *Service) SetStatus(serverID, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.UpdateServerStatus(ctx, serverID, status); err != nil {
		slog.Warn("lifecycle: status write", "server", serverID, "status", status, "error", err)
	}
	s.publishStatus(serverID, status, "")
}

func (s *Service) publishStatus(serverID, status, cause string) {
	if s.bus == nil {
		return
	}
	data := map[string]any{"status": status}
	if cause != "" {
		data["exit_cause"] = cause
	}
	s.bus.Publish(events.Event{Type: events.ServerStatus, ServerID: serverID, Data: data})
}

// startTimeout covers a first start that installs a runtime: Spigot's
// BuildTools compiles from source and can take well over ten minutes.
const startTimeout = 30 * time.Minute

// Start launches a server as an operation. The operation succeeds once the
// node has launched the process (after installing a runtime if needed); the
// boot itself is followed through the server's status.
func (s *Service) Start(ctx context.Context, srv *store.Server, actor operations.Actor) (*store.Operation, error) {
	if err := s.portClash(ctx, srv); err != nil {
		return nil, err
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return nil, err
	}
	actor.Audit(ctx, s.store, srv.ID, "server.start", nil)
	return s.ops.Run(ctx, srv.ID, operations.KindStart, actor, operations.Options{
		Timeout: startTimeout, Phase: "starting", Msg: "Starting the server",
	}, func(op *operations.Op) (any, error) {
		return s.launch(op, c, srv.ID, actor)
	})
}

// StartWithin starts a server as the last step of an operation that already
// holds it (applying a startup fix, say), where Start would be refused as busy.
func (s *Service) StartWithin(op *operations.Op, srv *store.Server, actor operations.Actor) error {
	c, err := s.agents(op.Context(), srv)
	if err != nil {
		return err
	}
	actor.Audit(op.Context(), s.store, srv.ID, "server.start", nil)
	_, err = s.launch(op, c, srv.ID, actor)
	return err
}

// launch is the body of a start: it re-reads the record, makes sure the node
// knows the directory, and asks it to start the server.
func (s *Service) launch(op *operations.Op, c Agent, serverID string, actor operations.Actor) (any, error) {
	s.SetStatus(serverID, "starting")
	// Re-read: the record may have changed between the request and now.
	current, err := s.store.GetServer(op.Context(), serverID)
	if err != nil {
		return nil, err
	}
	// Checked again here: another server may have started since the request,
	// and a start inside another operation (a fix) skips Start.
	if err := s.portClash(op.Context(), current); err != nil {
		s.SetStatus(serverID, "offline")
		return nil, err
	}
	if err := c.RegisterDir(op.Context(), current.ID, current.DirectoryPath); err != nil {
		s.startFailed(current, actor, err)
		return nil, fmt.Errorf("%w: %v", ErrNodeUnreachable, err)
	}
	op.Progress("launching", "Preparing the runtime and launching the server", nil)
	if err := c.StartServer(op.Context(), current.ID, agent.StartConfigForServer(current)); err != nil {
		if errors.Is(err, agent.ErrServerAlreadyRunning) {
			return map[string]string{"status": "already running"}, nil
		}
		if op.Cancelled() {
			// Dropping the request does not stop the node, which would finish
			// fetching the runtime and launch a server nobody wants any more.
			s.abandonStart(c, current.ID)
			return nil, err
		}
		s.startFailed(current, actor, err)
		return nil, err
	}
	return map[string]string{"status": "starting"}, nil
}

// abandonStart tells the node to drop a start the operation gave up on, before
// or after it launched.
func (s *Service) abandonStart(c Agent, serverID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.KillServer(ctx, serverID); err != nil && !errors.Is(err, agent.ErrNoProcess) {
		slog.Warn("lifecycle: abandon start", "server", serverID, "error", err)
	}
	s.SetStatus(serverID, "offline")
}

func (s *Service) startFailed(srv *store.Server, actor operations.Actor, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.SetStatus(srv.ID, "offline")
	actor.Audit(ctx, s.store, srv.ID, "server.start_failed", map[string]any{"error": err.Error()})
	_ = s.store.InsertLogEvent(ctx, srv.ID, "error", "Server failed to start: "+err.Error(), "api")
}

// StopOptions tune Stop.
type StopOptions struct {
	Graceful   bool
	TimeoutSec int
}

// Stop asks a server to stop. It refuses while an operation holds the server.
func (s *Service) Stop(ctx context.Context, srv *store.Server, actor operations.Actor, o StopOptions) error {
	if err := s.ops.Guard(ctx, srv.ID); err != nil {
		return err
	}
	if o.TimeoutSec <= 0 {
		o.TimeoutSec = 30
	}
	if o.TimeoutSec > 600 {
		o.TimeoutSec = 600
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(o.TimeoutSec+15)*time.Second)
	defer cancel()
	if err := c.StopServer(callCtx, srv.ID, o.Graceful, o.TimeoutSec); err != nil {
		return err
	}
	s.SetStatus(srv.ID, "stopping")
	actor.Audit(ctx, s.store, srv.ID, "server.stop", map[string]any{"graceful": o.Graceful})
	return nil
}

// Restart restarts a server with its current configuration, so a setting
// changed since the last start (the helper mod, say) is applied.
func (s *Service) Restart(ctx context.Context, srv *store.Server, actor operations.Actor) error {
	if err := s.ops.Guard(ctx, srv.ID); err != nil {
		return err
	}
	// Restarting a stopped server starts it.
	if err := s.portClash(ctx, srv); err != nil {
		return err
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if err := c.RegisterDir(callCtx, srv.ID, srv.DirectoryPath); err != nil {
		return fmt.Errorf("%w: %v", ErrNodeUnreachable, err)
	}
	if err := c.RestartServer(callCtx, srv.ID, agent.StartConfigForServer(srv)); err != nil {
		return err
	}
	s.SetStatus(srv.ID, "starting")
	actor.Audit(ctx, s.store, srv.ID, "server.restart", nil)
	return nil
}

// Kill ends a server's process at once. While an operation holds the server it
// refuses unless force is set, in which case the operation is cancelled first:
// killing a server mid-boot under a safe update would otherwise read to that
// update as a broken boot, and it would revert good versions.
func (s *Service) Kill(ctx context.Context, srv *store.Server, actor operations.Actor, force bool) error {
	if err := s.ops.Guard(ctx, srv.ID); err != nil {
		if _, busy := store.AsServerBusy(err); !busy || !force {
			return err
		}
		if cur, ok := s.ops.CancelServer(ctx, srv.ID); ok {
			actor.Audit(ctx, s.store, srv.ID, "operation.cancelled", map[string]any{"operation_id": cur.ID, "kind": cur.Kind})
		}
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	// An agent with no process for the server has nothing to kill: the server
	// is already down, which is what the kill asked for.
	if err := c.KillServer(callCtx, srv.ID); err != nil && !errors.Is(err, agent.ErrNoProcess) {
		return err
	}
	s.SetStatus(srv.ID, "offline")
	actor.Audit(ctx, s.store, srv.ID, "server.kill", map[string]any{"forced": force})
	return nil
}

// ReinstallTarget names the runtime to install. A zero value reinstalls the
// server's current runtime.
type ReinstallTarget struct {
	Platform      string
	MCVersion     string
	LoaderVersion *string
}

// Reinstall stops the server, installs the requested runtime and only then
// records the new version, as an operation detached from the request: leaving
// the page no longer abandons a half-recorded install.
func (s *Service) Reinstall(ctx context.Context, srv *store.Server, target ReinstallTarget, actor operations.Actor) (*store.Operation, error) {
	c, err := s.agents(ctx, srv)
	if err != nil {
		return nil, err
	}
	return s.ops.Run(ctx, srv.ID, operations.KindReinstall, actor, operations.Options{
		Timeout: startTimeout, Phase: "stopping", Msg: "Stopping the server",
	}, func(op *operations.Op) (any, error) {
		octx := op.Context()
		current, err := s.store.GetServer(octx, srv.ID)
		if err != nil {
			return nil, err
		}
		next := *current
		if target.Platform != "" {
			next.Platform, next.MCVersion, next.LoaderVersion = target.Platform, target.MCVersion, target.LoaderVersion
		}
		if err := c.RegisterDir(octx, current.ID, current.DirectoryPath); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNodeUnreachable, err)
		}
		stopCtx, cancel := context.WithTimeout(octx, 90*time.Second)
		err = StopAndWait(stopCtx, c, current.ID)
		cancel()
		if err != nil {
			return nil, err
		}
		op.Progress("installing", fmt.Sprintf("Installing %s %s", next.Platform, next.MCVersion), nil)
		cfg := map[string]any{
			"directory":   next.DirectoryPath,
			"platform":    next.Platform,
			"mc_version":  next.MCVersion,
			"java_binary": next.JavaBinary,
		}
		if next.LoaderVersion != nil && *next.LoaderVersion != "" {
			cfg["loader_version"] = *next.LoaderVersion
		}
		if err := c.Reinstall(octx, current.ID, cfg); err != nil {
			return nil, err
		}
		next.Settings = WithoutImportSettings(next.Settings)
		if err := s.store.UpdateServer(octx, current.ID, &next); err != nil {
			return nil, fmt.Errorf("the runtime was installed but the server record could not be updated: %w", err)
		}
		s.SetStatus(current.ID, "offline")
		actor.Audit(octx, s.store, current.ID, "server.reinstall", map[string]any{
			"platform": next.Platform, "mc_version": next.MCVersion, "loader_version": next.LoaderVersion,
		})
		return map[string]string{"platform": next.Platform, "mc_version": next.MCVersion}, nil
	})
}

// DeleteOptions say what to remove along with the server's record.
type DeleteOptions struct {
	Files   bool
	Backups bool
	// Force deletes the record even when the node cannot be reached, and
	// cancels an operation holding the server.
	Force bool
}

// Delete removes a server. The process is always stopped on the node first, so
// a deleted server is never left running with no way to stop it; when the
// node cannot be reached the record is kept unless Force is set.
func (s *Service) Delete(ctx context.Context, srv *store.Server, actor operations.Actor, o DeleteOptions) error {
	if err := s.ops.Guard(ctx, srv.ID); err != nil {
		if !o.Force {
			return err
		}
		s.ops.CancelServer(ctx, srv.ID)
	}
	c, err := s.agents(ctx, srv)
	if err == nil {
		// Stop gracefully first so the world is saved; the purge that follows
		// kills whatever is left, then forgets the server on the node.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		err = StopAndWait(stopCtx, c, srv.ID)
		cancel()
		if err == nil {
			callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			err = c.PurgeServer(callCtx, srv.ID, srv.DirectoryPath, o.Files, o.Backups)
			cancel()
		}
	}
	if err != nil && !o.Force {
		if agent.Unreachable(err) {
			return fmt.Errorf("%w; the server was not deleted: %v", ErrNodeUnreachable, err)
		}
		return fmt.Errorf("the server was not deleted: %w", err)
	}
	// Recorded against the server while it still exists, so the row carries
	// its name and closes the server's own history; audit rows outlive it.
	actor.Audit(ctx, s.store, srv.ID, "server.delete", map[string]any{
		"name": srv.Name, "deleted_files": o.Files, "deleted_backups": o.Backups,
		"forced": o.Force, "node_reached": err == nil,
	})
	if err := s.store.DeleteServer(ctx, srv.ID); err != nil {
		return err
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: events.ServerDeleted, ServerID: srv.ID})
	}
	return nil
}

// StopAndWait stops a server and waits until its process has exited. crashed
// and startup_failure are terminal states too: nothing resets them until the
// next start, so waiting for "offline" there would never end.
func StopAndWait(ctx context.Context, c Agent, serverID string) error {
	info, err := c.GetStatus(ctx, serverID)
	if err != nil {
		return fmt.Errorf("check server status: %w", err)
	}
	if ProcessDead(info["status"]) {
		return nil
	}
	if err := c.StopServer(ctx, serverID, true, 30); err != nil {
		info, statusErr := c.GetStatus(ctx, serverID)
		if statusErr == nil && ProcessDead(info["status"]) {
			return nil
		}
		return fmt.Errorf("stop server: %w", err)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for server to stop: %w", ctx.Err())
		case <-ticker.C:
			info, err := c.GetStatus(ctx, serverID)
			if err != nil {
				return fmt.Errorf("check stopped server status: %w", err)
			}
			if ProcessDead(info["status"]) {
				return nil
			}
		}
	}
}

// ProcessDead reports whether an agent status means the JVM has exited.
func ProcessDead(status any) bool {
	switch status {
	case "offline", "crashed", "startup_failure":
		return true
	}
	return false
}
