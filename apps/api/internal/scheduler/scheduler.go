// Package scheduler runs ScheduledTask rows on their configured cron
// expression. It refreshes its registry from the database periodically so
// new/edited/disabled tasks pick up without restart.
package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/backups"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// ModUpdater triggers a safe auto-update run for a server. Satisfied by
// *autoupdate.Engine; the run itself is asynchronous.
type ModUpdater interface {
	Trigger(ctx context.Context, serverID, trigger string) (*store.ModUpdateRun, error)
}

type Scheduler struct {
	cron       *cron.Cron
	store      *store.Store
	updater    ModUpdater
	engine     *notify.Engine
	refreshInt time.Duration

	mu      sync.Mutex
	entries map[string]registration // task ID -> registration
}

type registration struct {
	entryID  cron.EntryID
	cronExpr string
	action   string
}

func New(s *store.Store, updater ModUpdater, engine *notify.Engine) *Scheduler {
	return &Scheduler{
		// SecondField off; we use 5-field cron expressions like "0 4 * * *".
		cron:       cron.New(),
		store:      s,
		updater:    updater,
		engine:     engine,
		refreshInt: 30 * time.Second,
		entries:    map[string]registration{},
	}
}

// Start begins running tasks. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
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

		// If the cron expr or action changed, re-register.
		if reg, ok := s.entries[t.ID]; ok {
			if reg.cronExpr == t.CronExpr && reg.action == t.Action {
				continue
			}
			s.cron.Remove(reg.entryID)
			delete(s.entries, t.ID)
		}

		task := t // capture
		entryID, err := s.cron.AddFunc(task.CronExpr, func() {
			s.runTask(task)
		})
		if err != nil {
			log.Printf("scheduler: add %s (%s): %v", task.Name, task.CronExpr, err)
			continue
		}
		s.entries[task.ID] = registration{entryID: entryID, cronExpr: task.CronExpr, action: task.Action}
		log.Printf("scheduler: registered %q [%s] %s", task.Name, task.CronExpr, task.Action)
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
		next := store.NextRunForCron(t.CronExpr, now)
		if next == nil {
			continue
		}
		if t.NextRun == nil || !t.NextRun.Equal(*next) {
			if err := s.store.SetTaskNextRun(ctx, t.ID, next); err != nil {
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

func (s *Scheduler) runTask(task *store.ScheduledTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	now := time.Now()
	defer func() {
		// Compute next_run from the cron entry, if still registered.
		s.mu.Lock()
		reg, ok := s.entries[task.ID]
		s.mu.Unlock()
		var next *time.Time
		if ok {
			if e := s.cron.Entry(reg.entryID); e.Valid() {
				n := e.Next
				next = &n
			}
		}
		_ = s.store.UpdateTaskLastRun(ctx, task.ID, now, next)
	}()

	srv, err := s.store.GetServer(ctx, task.ServerID)
	if err != nil {
		log.Printf("scheduler: task %s server lookup: %v", task.ID, err)
		return
	}

	// Re-authorize at fire time: a task must never outlive its creator's ability
	// to perform its action. If the creator was deleted or lost the permission the
	// action requires, the task is skipped (not run with the server's authority).
	if !s.creatorAuthorized(ctx, task, srv.ID) {
		log.Printf("scheduler: task %q skipped — creator no longer authorized for %q", task.Name, task.Action)
		return
	}

	// mod_update runs through the auto-update engine (which manages its own
	// agent connection and run lifecycle), not the per-task agent client.
	if task.Action == "mod_update" {
		if s.updater == nil {
			log.Printf("scheduler: task %s skipped (no auto-update engine)", task.Name)
			return
		}
		run, err := s.updater.Trigger(ctx, srv.ID, "scheduled")
		if err != nil {
			log.Printf("scheduler: task %s auto-update: %v", task.Name, err)
			return
		}
		log.Printf("scheduler: task %q started auto-update run %s", task.Name, run.ID)
		return
	}

	node, err := s.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		log.Printf("scheduler: task %s node lookup: %v", task.ID, err)
		return
	}

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		log.Printf("scheduler: task %s register dir: %v", task.ID, err)
		return
	}

	switch task.Action {
	case "command":
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(task.Payload, &p)
		if p.Command == "" {
			log.Printf("scheduler: task %s skipped (empty command)", task.Name)
			return
		}
		if err := c.SendCommand(ctx, srv.ID, p.Command); err != nil {
			log.Printf("scheduler: task %s command: %v", task.Name, err)
		}

	case "restart":
		if err := c.RestartServer(ctx, srv.ID); err != nil {
			log.Printf("scheduler: task %s restart: %v", task.Name, err)
		}

	case "stop":
		if err := c.StopServer(ctx, srv.ID, true, 30); err != nil {
			log.Printf("scheduler: task %s stop: %v", task.Name, err)
		}

	case "backup":
		b := &store.Backup{
			ServerID: srv.ID,
			Trigger:  "scheduled",
			Status:   "running",
		}
		created, err := s.store.CreateBackup(ctx, b)
		if err != nil {
			log.Printf("scheduler: task %s create backup row: %v", task.Name, err)
			return
		}
		result, berr := c.Backup(ctx, srv.ID, created.ID)
		if berr != nil {
			_ = s.store.UpdateBackupResult(ctx, created.ID, "failed", nil, berr.Error())
			log.Printf("scheduler: task %s backup: %v", task.Name, berr)
			s.engine.Emit(notify.BackupFailed(srv.ID, srv.Name, berr.Error()))
			return
		}
		_ = s.store.UpdateBackupResult(ctx, created.ID, "success", &result.SizeBytes, "")
		backups.Enforce(ctx, s.store, srv.ID)
		s.engine.Emit(notify.BackupSuccess(srv.ID, srv.Name))

	default:
		log.Printf("scheduler: task %s unknown action %q", task.Name, task.Action)
	}
}
