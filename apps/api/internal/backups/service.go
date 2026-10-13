package backups

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// Agent is the slice of the node client backups need.
type Agent interface {
	RegisterDir(ctx context.Context, serverID, directory string) error
	Backup(ctx context.Context, serverID, backupID string) (*agent.BackupResult, error)
	Restore(ctx context.Context, serverID, backupID string) error
	DeleteBackup(ctx context.Context, serverID, backupID string) error
}

// AgentResolver returns a client for the node a server lives on.
type AgentResolver func(ctx context.Context, srv *store.Server) (Agent, error)

// Service creates, restores and deletes backups: the one implementation the
// HTTP API, the scheduler, MCP and the version-migration engine share.
type Service struct {
	store    *store.Store
	ops      *operations.Manager
	bus      *events.Bus
	notifier *notify.Engine
	agents   AgentResolver
}

// NewService builds the service. agents may be nil to resolve real agents.
func NewService(s *store.Store, ops *operations.Manager, bus *events.Bus, notifier *notify.Engine, agents AgentResolver) *Service {
	if agents == nil {
		agents = func(ctx context.Context, srv *store.Server) (Agent, error) {
			node, err := s.GetNode(ctx, srv.NodeID)
			if err != nil {
				return nil, fmt.Errorf("node not found")
			}
			return agent.New(node.Scheme, node.FQDN, node.Port, node.Token), nil
		}
	}
	return &Service{store: s, ops: ops, bus: bus, notifier: notifier, agents: agents}
}

// backupTimeout bounds one backup. A multi-gigabyte world can take a while to
// compress; this is generous so a large world is never reported failed while
// the node is still writing it.
const backupTimeout = 6 * time.Hour

// Create starts a backup as an operation. It returns the backup record (still
// running) and the operation at once.
func (s *Service) Create(ctx context.Context, srv *store.Server, actor operations.Actor, trigger string) (*store.Backup, *store.Operation, error) {
	var created *store.Backup
	ready := make(chan error, 1)
	op, err := s.ops.Run(ctx, srv.ID, operations.KindBackup, actor, operations.Options{
		Timeout: backupTimeout, Phase: "preparing", Msg: "Preparing the backup",
	}, func(op *operations.Op) (any, error) {
		b, err := s.begin(op.Context(), srv, actor, trigger)
		if err != nil {
			ready <- err
			return nil, err
		}
		created = b
		op.SetRef(b.ID)
		ready <- nil
		return s.finish(op.Context(), op, srv, b)
	})
	if err != nil {
		return nil, nil, err
	}
	if err := <-ready; err != nil {
		return nil, op, err
	}
	actor.Audit(ctx, s.store, srv.ID, "backup.create", map[string]any{"backup_id": created.ID, "operation_id": op.ID})
	return created, op, nil
}

// Take makes a backup synchronously inside an operation that already holds
// the server (a version migration, a restore's safety copy).
func (s *Service) Take(ctx context.Context, op *operations.Op, srv *store.Server, actor operations.Actor, trigger string) (*store.Backup, error) {
	b, err := s.begin(ctx, srv, actor, trigger)
	if err != nil {
		return nil, err
	}
	if _, err := s.finish(ctx, op, srv, b); err != nil {
		return b, err
	}
	actor.Audit(ctx, s.store, srv.ID, "backup.create", map[string]any{"backup_id": b.ID, "trigger": trigger})
	return b, nil
}

func (s *Service) begin(ctx context.Context, srv *store.Server, actor operations.Actor, trigger string) (*store.Backup, error) {
	var by *string
	if actor.UserID != "" {
		by = &actor.UserID
	}
	return s.store.CreateBackup(ctx, &store.Backup{
		ServerID: srv.ID, TriggeredBy: by, Trigger: trigger, Status: "running",
	})
}

// finish zips the server on its node and records the outcome. Retention runs
// only after a success.
func (s *Service) finish(ctx context.Context, op *operations.Op, srv *store.Server, b *store.Backup) (any, error) {
	fail := func(err error) (any, error) {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.store.UpdateBackupResult(cctx, b.ID, "failed", nil, err.Error())
		s.notifier.Emit(notify.BackupFailed(srv.ID, srv.Name, err.Error()))
		return nil, err
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return fail(err)
	}
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		return fail(fmt.Errorf("the node could not be reached: %w", err))
	}
	if op != nil {
		op.Progress("archiving", "Saving the world and archiving the server", nil)
	}
	result, err := c.Backup(ctx, srv.ID, b.ID)
	if err != nil {
		return fail(err)
	}
	if err := s.store.UpdateBackupResult(ctx, b.ID, "success", &result.SizeBytes, ""); err != nil {
		return nil, err
	}
	Enforce(ctx, s.store, srv.ID)
	s.notifier.Emit(notify.BackupSuccess(srv.ID, srv.Name))
	if result.Warning != "" {
		_ = s.store.InsertLogEvent(ctx, srv.ID, "warning", result.Warning, "backup")
		nodeName := ""
		if node, nerr := s.store.GetNode(ctx, srv.NodeID); nerr == nil {
			nodeName = node.Name
		}
		s.notifier.Emit(notify.NodeDiskLow(srv.NodeID, nodeName, result.Warning))
	}
	return map[string]any{"backup_id": b.ID, "size_bytes": result.SizeBytes}, nil
}

// ErrBackupNotFound means no successful backup with that id belongs to the
// server.
var ErrBackupNotFound = errors.New("backup not found")

// RestoreOptions tune Restore.
type RestoreOptions struct {
	// SkipSafetyBackup restores without first backing up the current world.
	// The safety copy is what makes a wrong choice recoverable, so it is on
	// unless the caller says otherwise.
	SkipSafetyBackup bool
}

// Restore replaces the server's directory with a backup, as an operation. The
// current state is backed up first, so restoring the wrong backup loses
// nothing.
func (s *Service) Restore(ctx context.Context, srv *store.Server, backupID string, actor operations.Actor, o RestoreOptions) (*store.Operation, error) {
	b, err := s.store.GetBackup(ctx, backupID)
	if err != nil || b.ServerID != srv.ID {
		return nil, ErrBackupNotFound
	}
	if b.Status != "success" {
		return nil, fmt.Errorf("can only restore a successful backup")
	}
	c, err := s.agents(ctx, srv)
	if err != nil {
		return nil, err
	}
	actor.Audit(ctx, s.store, srv.ID, "backup.restore", map[string]any{"backup_id": backupID})
	return s.ops.Run(ctx, srv.ID, operations.KindRestore, actor, operations.Options{
		Timeout: backupTimeout, Phase: "preparing", Msg: "Preparing the restore",
	}, func(op *operations.Op) (any, error) {
		octx := op.Context()
		op.SetRef(backupID)
		result := map[string]any{"restored": backupID}
		if !o.SkipSafetyBackup {
			op.Progress("safety_backup", "Backing up the current world first", nil)
			safety, err := s.Take(octx, op, srv, actor, "pre_restore")
			if err != nil {
				return nil, fmt.Errorf("the safety backup of the current world failed, so nothing was restored: %w", err)
			}
			result["safety_backup_id"] = safety.ID
		}
		op.Progress("restoring", "Stopping the server and restoring the backup", nil)
		if err := c.RegisterDir(octx, srv.ID, srv.DirectoryPath); err != nil {
			return nil, fmt.Errorf("the node could not be reached: %w", err)
		}
		if err := c.Restore(octx, srv.ID, backupID); err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.store.UpdateServerStatus(cctx, srv.ID, "offline")
		cancel()
		if s.bus != nil {
			s.bus.Publish(events.Event{Type: events.ServerStatus, ServerID: srv.ID,
				Data: map[string]any{"status": "offline", "exit_cause": "requested"}})
		}
		return result, nil
	})
}

// Delete removes a backup's archive and record. It refuses while an operation
// holds the server, so a backup cannot vanish in the middle of being restored.
func (s *Service) Delete(ctx context.Context, srv *store.Server, backupID string, actor operations.Actor) error {
	b, err := s.store.GetBackup(ctx, backupID)
	if err != nil || b.ServerID != srv.ID {
		return ErrBackupNotFound
	}
	if b.Status == "running" {
		return &store.ErrServerBusy{}
	}
	if err := s.ops.Guard(ctx, srv.ID); err != nil {
		return err
	}
	if b.Status == "success" {
		c, err := s.agents(ctx, srv)
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := c.RegisterDir(cctx, srv.ID, srv.DirectoryPath); err != nil {
			return fmt.Errorf("the node could not be reached: %w", err)
		}
		if err := c.DeleteBackup(cctx, srv.ID, backupID); err != nil {
			return err
		}
	}
	if err := s.store.DeleteBackup(ctx, backupID); err != nil {
		return err
	}
	actor.Audit(ctx, s.store, srv.ID, "backup.delete", map[string]any{"backup_id": backupID, "status": b.Status})
	return nil
}

// CreateAndWait takes a backup as an operation and returns when it has
// finished, for callers (the scheduler) that record the outcome themselves.
func (s *Service) CreateAndWait(ctx context.Context, srv *store.Server, actor operations.Actor, trigger string) (*store.Backup, *store.Operation, error) {
	op, err := s.ops.Begin(ctx, srv.ID, operations.KindBackup, actor, operations.Options{
		Timeout: backupTimeout, Phase: "preparing", Msg: "Preparing the backup",
	})
	if err != nil {
		return nil, nil, err
	}
	rec := op.Record()
	b, err := s.Take(op.Context(), op, srv, actor, trigger)
	if b != nil {
		op.SetRef(b.ID)
	}
	var result any
	if b != nil && err == nil {
		result = map[string]any{"backup_id": b.ID}
	}
	op.Done(err, result)
	return b, &rec, err
}
