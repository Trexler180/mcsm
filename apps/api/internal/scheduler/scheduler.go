// Package scheduler runs ScheduledTask rows on their configured cron
// expression. It refreshes its registry from the database periodically so
// new/edited/disabled tasks pick up without restart.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/backups"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/lifecycle"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// ModUpdater triggers a safe auto-update run for a server. Satisfied by
// *autoupdate.Engine; the run itself is asynchronous.
type ModUpdater interface {
	Trigger(ctx context.Context, serverID, trigger string) (*store.ModUpdateRun, error)
}

// Parser is the cron dialect tasks use: standard five fields plus the
// @hourly/@daily/@every descriptors. Handlers validate with it so a stored
// expression is always one the scheduler can run.
var Parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// MinInterval is the shortest gap a task may fire at. Anything faster is a
// mistake or abuse (a command every second floods the console).
const MinInterval = time.Minute

// ValidateCron checks an expression parses and does not fire more often than
// MinInterval.
func ValidateCron(expr string) error {
	sched, err := Parser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid schedule: %v", err)
	}
	now := time.Now()
	first := sched.Next(now)
	second := sched.Next(first)
	if !first.IsZero() && second.Sub(first) < MinInterval {
		return fmt.Errorf("a task may not run more often than once a minute")
	}
	return nil
}

type Scheduler struct {
	cron       *cron.Cron
	store      *store.Store
	life       *lifecycle.Service
	backups    *backups.Service
	updater    ModUpdater
	engine     *notify.Engine
	bus        *events.Bus
	refreshInt time.Duration

	mu      sync.Mutex
	entries map[string]registration // task ID -> registration
}

type registration struct {
	entryID cron.EntryID
	// fingerprint covers everything that decides what the task does, so an
	// edit to its command text or its owner re-registers it — not only a
	// change of schedule or action.
	fingerprint string
}

// New builds a scheduler. life and backups may be nil to build private ones
// (tests); production shares the router's so the operation lock is one lock.
func New(s *store.Store, updater ModUpdater, engine *notify.Engine, life *lifecycle.Service, bk *backups.Service, bus *events.Bus) *Scheduler {
	if life == nil || bk == nil {
		ops := operations.New(s, bus)
		if life == nil {
			life = lifecycle.New(s, ops, bus, nil)
		}
		if bk == nil {
			bk = backups.NewService(s, ops, bus, engine, nil)
		}
	}
	return &Scheduler{
		// A task still running when its next firing comes round is skipped
		// rather than run twice at once (a backup that outlasts its interval).
		cron:       cron.New(cron.WithParser(Parser), cron.WithChain(cron.SkipIfStillRunning(cron.DiscardLogger))),
		store:      s,
		life:       life,
		backups:    bk,
		updater:    updater,
		engine:     engine,
		bus:        bus,
		refreshInt: 30 * time.Second,
		entries:    map[string]registration{},
	}
}

// Start begins running tasks. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	if err := s.store.InterruptRunningTaskRuns(ctx); err != nil {
		log.Printf("scheduler: close interrupted runs: %v", err)
	}
	s.cron.Start()
	s.refresh(ctx)
	go s.refreshLoop(ctx)
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

func (s *Scheduler) refreshLoop(ctx context.Context) {
	t := time.NewTicker(s.refreshInt)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func fingerprint(t *store.ScheduledTask) string {
	creator := ""
	if t.CreatedBy != nil {
		creator = *t.CreatedBy
	}
	return strings.Join([]string{t.CronExpr, t.Action, string(t.Payload), creator, t.Name}, "\x00")
}

func (s *Scheduler) refresh(ctx context.Context) {
	tasks, err := s.store.ListAllEnabledTasks(ctx)
	if err != nil {
		log.Printf("scheduler: list tasks: %v", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seen := map[string]bool{}
	for _, t := range tasks {
		seen[t.ID] = true
		fp := fingerprint(t)

		if reg, ok := s.entries[t.ID]; ok {
			if reg.fingerprint == fp {
				continue
			}
			s.cron.Remove(reg.entryID)
			delete(s.entries, t.ID)
		}

		taskID := t.ID
		entryID, err := s.cron.AddFunc(t.CronExpr, func() {
			s.fire(taskID)
		})
		if err != nil {
			log.Printf("scheduler: add %s (%s): %v", t.Name, t.CronExpr, err)
			continue
		}
		s.entries[t.ID] = registration{entryID: entryID, fingerprint: fp}
		log.Printf("scheduler: registered %q [%s] %s", t.Name, t.CronExpr, t.Action)
	}

	// Drop entries that are no longer enabled / present.
	for tid, reg := range s.entries {
		if !seen[tid] {
			s.cron.Remove(reg.entryID)
			delete(s.entries, tid)
		}
	}

	// Keep each enabled task's cached next_run pointing at its true next
	// occurrence. Without this it's only written after a task fires, so a new
	// task (or one whose stored time went stale across an API restart) shows a
	// past time — rendered in the UI as "in now".
	now := time.Now()
	for _, t := range tasks {
		sched, err := Parser.Parse(t.CronExpr)
		if err != nil {
			continue
		}
		next := sched.Next(now)
		if t.NextRun == nil || !t.NextRun.Equal(next) {
			if err := s.store.SetTaskNextRun(ctx, t.ID, &next); err != nil {
				log.Printf("scheduler: set next_run for %s: %v", t.ID, err)
			}
		}
	}
}

// creatorAuthorized reports whether the task's creator may still run its action
// on the server. Legacy tasks (no creator recorded) are allowed; a deleted
// creator is denied (their lookup fails). Global admins always pass; otherwise
// the creator must hold the per-server permission the action maps to.
func (s *Scheduler) creatorAuthorized(ctx context.Context, task *store.ScheduledTask, serverID string) bool {
	if task.CreatedBy == nil {
		return true
	}
	user, err := s.store.GetUserByID(ctx, *task.CreatedBy)
	if err != nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	needed, ok := store.RequiredTaskPermission(task.Action)
	if !ok {
		return false
	}
	allowed, err := s.store.UserHasServerPermission(ctx, *task.CreatedBy, serverID, needed)
	return err == nil && allowed
}

// fire runs a task by id. It re-reads the task, so it always runs the task as
// it is now, not as it was when it was registered.
func (s *Scheduler) fire(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Hour)
	defer cancel()
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil || !task.Enabled {
		return
	}
	s.runTask(ctx, task)
}

func (s *Scheduler) runTask(ctx context.Context, task *store.ScheduledTask) {
	now := time.Now()
	defer s.recordLastRun(task, now)

	srv, run, ok := s.begin(ctx, task)
	if !ok {
		return
	}
	// Re-authorize at fire time: a task must never outlive its creator's ability
	// to perform its action. If the creator was deleted or lost the permission the
	// action requires, the task is skipped (not run with the server's authority).
	if !s.creatorAuthorized(ctx, task, srv.ID) {
		s.finish(task, srv, run, "skipped", "the task's creator no longer has the permission this action needs", "")
		return
	}
	actor := operations.Actor{Trigger: operations.TriggerScheduled, TaskID: task.ID, RunID: run.ID}
	if task.CreatedBy != nil {
		actor.UserID = *task.CreatedBy
	}
	s.execute(ctx, task, srv, run, actor)
}

// RunNow runs a task straight away on behalf of userID, whom the caller has
// already checked may perform its action. It answers once the run is
// recorded; the work carries on in the background, and its outcome arrives
// as task.run events and in the task's history like any other run.
func (s *Scheduler) RunNow(task *store.ScheduledTask, userID string) (*store.TaskRun, error) {
	ctx := context.Background()
	srv, err := s.store.GetServer(ctx, task.ServerID)
	if err != nil {
		return nil, err
	}
	run, err := s.store.StartTaskRun(ctx, task.ID, srv.ID, task.Action)
	if err != nil {
		return nil, err
	}
	s.publish(srv.ID, task, run.ID, "running", "")
	actor := operations.Actor{Trigger: operations.TriggerManual, TaskID: task.ID, RunID: run.ID, UserID: userID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Hour)
		defer cancel()
		defer s.recordLastRun(task, time.Now())
		s.execute(ctx, task, srv, run, actor)
	}()
	return run, nil
}

// recordLastRun stamps when the task ran and when it runs next.
func (s *Scheduler) recordLastRun(task *store.ScheduledTask, ran time.Time) {
	var next *time.Time
	if sched, err := Parser.Parse(task.CronExpr); err == nil {
		n := sched.Next(time.Now())
		next = &n
	}
	_ = s.store.UpdateTaskLastRun(context.Background(), task.ID, ran, next)
}

// begin looks up the task's server and records that the task fired.
func (s *Scheduler) begin(ctx context.Context, task *store.ScheduledTask) (*store.Server, *store.TaskRun, bool) {
	srv, err := s.store.GetServer(ctx, task.ServerID)
	if err != nil {
		log.Printf("scheduler: task %s server lookup: %v", task.ID, err)
		return nil, nil, false
	}
	run, err := s.store.StartTaskRun(ctx, task.ID, srv.ID, task.Action)
	if err != nil {
		log.Printf("scheduler: record run of %s: %v", task.ID, err)
		return nil, nil, false
	}
	s.publish(srv.ID, task, run.ID, "running", "")
	return srv, run, true
}

// record records how a run ended, without raising an alert.
func (s *Scheduler) record(task *store.ScheduledTask, srv *store.Server, run *store.TaskRun, status, message, operationID string) {
	_ = s.store.FinishTaskRun(context.Background(), run.ID, status, message, operationID)
	s.publish(srv.ID, task, run.ID, status, message)
}

// finish records how a run ended, and raises an alert when it didn't happen.
func (s *Scheduler) finish(task *store.ScheduledTask, srv *store.Server, run *store.TaskRun, status, message, operationID string) {
	s.record(task, srv, run, status, message, operationID)
	if status == "failed" || status == "skipped" {
		log.Printf("scheduler: task %q %s: %s", task.Name, status, message)
		s.engine.Emit(notify.TaskFailed(srv.ID, srv.Name, task.Name, message))
	}
}

// execute does what the task is for, as actor.
func (s *Scheduler) execute(ctx context.Context, task *store.ScheduledTask, srv *store.Server, run *store.TaskRun, actor operations.Actor) {
	finish := func(status, message, operationID string) { s.finish(task, srv, run, status, message, operationID) }

	switch task.Action {
	case "mod_update":
		if s.updater == nil {
			finish("failed", "safe updates are unavailable", "")
			return
		}
		r, err := s.updater.Trigger(operations.ContextWithActor(ctx, actor), srv.ID, "scheduled")
		if err != nil {
			finish("failed", err.Error(), "")
			return
		}
		finish("succeeded", "started safe update run "+r.ID, "")

	case "command":
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(task.Payload, &p)
		if strings.TrimSpace(p.Command) == "" {
			finish("failed", "the task has no command", "")
			return
		}
		node, err := s.store.GetNode(ctx, srv.NodeID)
		if err != nil {
			finish("failed", "node not found", "")
			return
		}
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = c.SendCommand(cctx, srv.ID, p.Command)
		cancel()
		if errors.Is(err, agent.ErrServerNotRunning) {
			// A message for players, with no players to hear it: recorded,
			// but not worth an alert.
			s.record(task, srv, run, "skipped", "the server wasn't running", "")
			return
		}
		if err != nil {
			finish("failed", err.Error(), "")
			return
		}
		actor.Audit(ctx, s.store, srv.ID, "console.command", map[string]any{"command": p.Command, "task": task.Name})
		finish("succeeded", "", "")

	case "start":
		op, err := s.life.Start(ctx, srv, actor)
		if err != nil {
			finish("failed", err.Error(), "")
			return
		}
		finish("succeeded", "", op.ID)

	case "restart":
		// Restarting a stopped server would start it, undoing whoever stopped
		// it on purpose; a nightly restart only refreshes one that's running.
		if srv.Status != "online" && srv.Status != "starting" {
			s.record(task, srv, run, "skipped", "the server wasn't running", "")
			return
		}
		if err := s.life.Restart(ctx, srv, actor); err != nil {
			finish("failed", err.Error(), "")
			return
		}
		finish("succeeded", "", "")

	case "stop":
		if err := s.life.Stop(ctx, srv, actor, lifecycle.StopOptions{Graceful: true, TimeoutSec: 30}); err != nil {
			finish("failed", err.Error(), "")
			return
		}
		finish("succeeded", "", "")

	case "backup":
		_, op, err := s.backups.CreateAndWait(ctx, srv, actor, "scheduled")
		opID := ""
		if op != nil {
			opID = op.ID
		}
		if err != nil {
			finish("failed", err.Error(), opID)
			return
		}
		finish("succeeded", "", opID)

	default:
		finish("failed", fmt.Sprintf("unknown action %q", task.Action), "")
	}
}

func (s *Scheduler) publish(serverID string, task *store.ScheduledTask, runID, status, message string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{Type: events.TaskRun, ServerID: serverID, Data: map[string]any{
		"task_id": task.ID, "task_name": task.Name, "run_id": runID, "action": task.Action,
		"status": status, "message": message,
	}})
}
