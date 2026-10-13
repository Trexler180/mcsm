// Package operations runs long work against a server as a tracked job and
// holds the per-server lock that keeps such jobs from overlapping.
//
// A job outlives the request that started it: its context is detached and
// bounded by its own timeout, its progress is written to the operations table
// for the dashboard to follow, and its end is recorded whatever happens. An
// exclusive job holds its server, so every mutating path — power actions, mod
// changes, backups, restores, migrations, safe updates — either takes the lock
// or checks it with Guard.
package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/store"
)

// Kinds of operation.
const (
	KindStart      = "start"
	KindReinstall  = "reinstall"
	KindBackup     = "backup"
	KindRestore    = "restore"
	KindMigrate    = "migrate"
	KindAutoUpdate = "auto_update"
	KindWorldLoad  = "world_upload"
	KindDelete     = "delete"
	KindFix        = "fix"
)

// Triggers say who or what started an operation.
const (
	TriggerManual    = "manual"
	TriggerScheduled = "scheduled"
	TriggerAutomatic = "automatic"
	TriggerAgent     = "agent"
)

// Actor is who an operation is attributed to, for the operations table and
// the audit log. It carries no authority: callers authorize before acting.
type Actor struct {
	UserID     string
	KeyID      string
	GrantID    string
	ClientName string
	IP         string
	Trigger    string
	// TaskID and RunID name the scheduled task, and the run of it, behind a
	// scheduled action, so its audit rows lead back to the schedule.
	TaskID string
	RunID  string
}

type actorKey struct{}

// ContextWithActor carries an actor to code that starts work on someone's
// behalf without taking an Actor parameter (the update and migration engines).
func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFromContext returns the actor set by ContextWithActor.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// System is the actor for work the panel does on its own (autostart, a
// detected crash) with no person or schedule behind it.
var System = Actor{Trigger: TriggerAutomatic}

// ActorFromRequest attributes an operation to whoever made the request: an
// access key, an MCP delegation, or a signed-in person.
func ActorFromRequest(r *http.Request, ip string) Actor {
	a := Actor{IP: ip, Trigger: TriggerManual}
	if d := auth.DelegatedActorFrom(r.Context()); d != nil {
		a.UserID, a.GrantID, a.ClientName, a.Trigger = d.UserID, d.GrantID, d.ClientName, TriggerAgent
		if d.IP != "" {
			a.IP = d.IP
		}
		return a
	}
	if c := auth.ClaimsFrom(r.Context()); c != nil {
		a.UserID = c.UserID
	}
	if m := auth.MachineFrom(r.Context()); m != nil {
		a.KeyID = m.KeyID
		a.Trigger = TriggerAgent
	}
	return a
}

// Audit records an action under this actor.
func (a Actor) Audit(ctx context.Context, s *store.Store, serverID, action string, detail any) {
	if a.GrantID != "" {
		_ = s.LogActionWithActor(ctx, a.UserID, "", a.GrantID, serverID, action, a.IP, map[string]any{
			"client_name": a.ClientName, "operation": detail,
		})
		return
	}
	if a.Trigger == TriggerScheduled || a.Trigger == TriggerAutomatic || a.TaskID != "" {
		if m, ok := detail.(map[string]any); ok || detail == nil {
			if m == nil {
				m = map[string]any{}
			}
			m["trigger"] = a.Trigger
			if a.TaskID != "" {
				m["task_id"] = a.TaskID
			}
			if a.RunID != "" {
				m["task_run_id"] = a.RunID
			}
			detail = m
		}
	}
	_ = s.LogActionWithActor(ctx, a.UserID, a.KeyID, "", serverID, action, a.IP, detail)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Manager starts, tracks and cancels operations.
type Manager struct {
	store *store.Store
	bus   *events.Bus

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func New(s *store.Store, bus *events.Bus) *Manager {
	return &Manager{store: s, bus: bus, cancels: map[string]context.CancelFunc{}}
}

// Op is a running operation.
type Op struct {
	m      *Manager
	rec    *store.Operation
	ctx    context.Context
	cancel context.CancelFunc
}

// Options tune Begin.
type Options struct {
	// Timeout bounds the whole operation. Zero means two hours.
	Timeout time.Duration
	// Shared lets the operation run beside an exclusive one (it still records
	// progress). Most operations are exclusive.
	Shared bool
	Phase  string
	Msg    string
}

// Begin records a running operation and returns it, or *store.ErrServerBusy
// when the server is held by another exclusive operation.
func (m *Manager) Begin(ctx context.Context, serverID, kind string, actor Actor, o Options) (*Op, error) {
	rec := &store.Operation{
		ServerID:     serverID,
		Kind:         kind,
		Exclusive:    !o.Shared,
		Trigger:      actor.Trigger,
		ActorUserID:  nilIfEmpty(actor.UserID),
		ActorKeyID:   nilIfEmpty(actor.KeyID),
		ActorGrantID: nilIfEmpty(actor.GrantID),
		Phase:        o.Phase,
		Message:      o.Msg,
	}
	if err := m.store.CreateOperation(ctx, rec); err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	opCtx, cancel := context.WithTimeout(context.Background(), timeout)
	m.mu.Lock()
	m.cancels[rec.ID] = cancel
	m.mu.Unlock()
	op := &Op{m: m, rec: rec, ctx: opCtx, cancel: cancel}
	m.publish(rec)
	return op, nil
}

// Run begins an operation and runs fn in the background, finishing the
// operation with fn's outcome. It returns the operation as recorded at start.
func (m *Manager) Run(ctx context.Context, serverID, kind string, actor Actor, o Options, fn func(op *Op) (any, error)) (*store.Operation, error) {
	op, err := m.Begin(ctx, serverID, kind, actor, o)
	if err != nil {
		return nil, err
	}
	snapshot := *op.rec
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("operation panicked", "operation", op.rec.ID, "kind", kind, "panic", rec)
				op.Done(fmt.Errorf("internal error"), nil)
			}
		}()
		result, err := fn(op)
		op.Done(err, result)
	}()
	return &snapshot, nil
}

// Guard returns *store.ErrServerBusy when an exclusive operation holds the
// server. Short actions that do not record an operation of their own (a stop,
// a mod toggle) call it so they cannot land in the middle of one.
func (m *Manager) Guard(ctx context.Context, serverID string) error {
	cur, err := m.store.RunningOperation(ctx, serverID)
	if err != nil {
		return err
	}
	if cur != nil {
		return &store.ErrServerBusy{Current: cur}
	}
	return nil
}

// Cancel asks a running operation to stop. The operation sees its context
// cancelled and is recorded as cancelled when it returns.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// CancelServer cancels whatever exclusive operation holds a server.
func (m *Manager) CancelServer(ctx context.Context, serverID string) (*store.Operation, bool) {
	cur, err := m.store.RunningOperation(ctx, serverID)
	if err != nil || cur == nil {
		return nil, false
	}
	return cur, m.Cancel(cur.ID)
}

// Sweep marks operations left running by a previous process as interrupted,
// and closes the records they were driving (a backup still "running" can then
// be deleted). Run once at startup, before anything begins a new operation.
func (m *Manager) Sweep(ctx context.Context) []*store.Operation {
	ops, err := m.store.InterruptRunningOperations(ctx)
	if err != nil {
		slog.Error("operations: sweep interrupted", "error", err)
		return nil
	}
	if n, err := m.store.InterruptRunningRecords(ctx); err != nil {
		slog.Error("operations: close interrupted records", "error", err)
	} else if n > 0 {
		slog.Warn("closed records left running by a restart", "count", n)
	}
	for _, op := range ops {
		slog.Warn("operation interrupted by restart", "operation", op.ID, "server", op.ServerID, "kind", op.Kind)
		m.publish(op)
	}
	return ops
}

func (m *Manager) publish(rec *store.Operation) {
	if m.bus == nil {
		return
	}
	copied := *rec
	m.bus.Publish(events.Event{Type: events.OperationUpdated, ServerID: rec.ServerID, Data: &copied})
}

// Record is the operation as last written.
func (op *Op) Record() store.Operation { return *op.rec }

// ID is the operation's id.
func (op *Op) ID() string { return op.rec.ID }

// Context is the operation's own context: detached from any request, bounded
// by the operation's timeout, cancelled by Manager.Cancel.
func (op *Op) Context() context.Context { return op.ctx }

// Progress records where the operation has got to. fraction is nil when there
// is no meaningful measure.
func (op *Op) Progress(phase, message string, fraction *float64) {
	op.rec.Phase, op.rec.Message, op.rec.Progress = phase, message, fraction
	op.rec.UpdatedAt = time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := op.m.store.UpdateOperationProgress(ctx, op.rec.ID, phase, message, fraction); err != nil {
		slog.Warn("operations: progress", "operation", op.rec.ID, "error", err)
	}
	op.m.publish(op.rec)
}

// SetRef links the operation to the record it produced.
func (op *Op) SetRef(ref string) {
	op.rec.RefID = &ref
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = op.m.store.SetOperationRef(ctx, op.rec.ID, ref)
}

// Cancelled reports whether the operation was asked to stop or ran out of time.
func (op *Op) Cancelled() bool { return op.ctx.Err() != nil }

// Done finishes the operation: succeeded when err is nil, cancelled when its
// context was cancelled, failed otherwise. Safe to call more than once; the
// first call wins.
func (op *Op) Done(err error, result any) {
	status := store.OpSucceeded
	msg := ""
	switch {
	case err == nil:
	case errors.Is(op.ctx.Err(), context.Canceled):
		status, msg = store.OpCancelled, "cancelled"
		if err != nil && !errors.Is(err, context.Canceled) {
			msg = err.Error()
		}
	default:
		status, msg = store.OpFailed, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ferr := op.m.store.FinishOperation(ctx, op.rec.ID, status, msg, result); ferr != nil {
		slog.Error("operations: finish", "operation", op.rec.ID, "error", ferr)
	}
	op.m.mu.Lock()
	delete(op.m.cancels, op.rec.ID)
	op.m.mu.Unlock()
	op.cancel()
	now := time.Now().UTC()
	op.rec.Status, op.rec.Error, op.rec.FinishedAt, op.rec.UpdatedAt = status, msg, &now, now
	op.m.publish(op.rec)
}
