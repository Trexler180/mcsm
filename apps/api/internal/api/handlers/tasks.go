package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/scheduler"
	"github.com/mcsm/api/internal/store"
)

type TaskHandlers struct {
	store  *store.Store
	runner TaskRunner
}

func NewTaskHandlers(s *store.Store) *TaskHandlers {
	return &TaskHandlers{store: s}
}

// TaskRunner runs a scheduled task straight away (the scheduler).
type TaskRunner interface {
	RunNow(task *store.ScheduledTask, userID string) (*store.TaskRun, error)
}

// WithRunner enables running a task by hand.
func (h *TaskHandlers) WithRunner(r TaskRunner) *TaskHandlers {
	h.runner = r
	return h
}

// RunNow runs a task now, as the person asking rather than its creator, so it
// needs the same permission scheduling it would: the one its action requires.
// It answers 202 with the run; the outcome follows on the event stream and in
// the task's history.
func (h *TaskHandlers) RunNow(w http.ResponseWriter, r *http.Request) {
	serverID, taskID := chi.URLParam(r, "id"), chi.URLParam(r, "taskId")
	if h.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "running a task by hand isn't available")
		return
	}
	task, err := h.store.GetTask(r.Context(), taskID)
	if err != nil || task.ServerID != serverID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if status, msg := h.authorizeAction(r, serverID, task.Action); status != 0 {
		writeError(w, status, msg)
		return
	}
	run, err := h.runner.RunNow(task, auth.ClaimsFrom(r.Context()).UserID)
	if err != nil {
		writeServerError(w, r, "run task", err)
		return
	}
	audit(h.store, r, serverID, "task.run", map[string]any{"task_id": task.ID, "name": task.Name, "action": task.Action})
	writeJSON(w, http.StatusAccepted, run)
}

// authorizeAction enforces that the caller may schedule the given action: the
// action must be recognized, and the caller must independently hold the
// permission that action requires. Without this, the broad `tasks` permission
// would let a collaborator schedule a `command` task and run arbitrary console
// commands they could not run directly — a privilege escalation. Returns an HTTP
// status + message to send on rejection (0 status = allowed).
func (h *TaskHandlers) authorizeAction(r *http.Request, serverID, action string) (int, string) {
	needed, ok := store.RequiredTaskPermission(action)
	if !ok {
		return http.StatusBadRequest, "unsupported task action"
	}
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		return http.StatusUnauthorized, "unauthorized"
	}
	// A key reaches this route on its `tasks` scope alone; the action it wants to
	// schedule arrives in the body. Its scopes must satisfy that action before any
	// user-side check, or a tasks-only key would schedule a `command` task and
	// borrow its owner's console — the same escalation this function exists to
	// prevent for collaborators, one level out.
	if machine := auth.MachineFrom(r.Context()); machine != nil {
		if !machine.AllowsServer(serverID) || !store.HasServerPermission(machine.Scopes, needed) {
			return http.StatusForbidden, "you don't have permission to schedule this action"
		}
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		return http.StatusInternalServerError, "authorization check failed"
	}
	if user.Role == "admin" {
		return 0, ""
	}
	has, err := h.store.UserHasServerPermission(r.Context(), claims.UserID, serverID, needed)
	if err != nil {
		return http.StatusInternalServerError, "authorization check failed"
	}
	if !has {
		return http.StatusForbidden, "you don't have permission to schedule this action"
	}
	return 0, ""
}

func (h *TaskHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tasks, err := h.store.ListTasks(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "list tasks", err)
		return
	}
	if tasks == nil {
		tasks = []*store.ScheduledTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

const maxTaskNameLength = 80

// validateTask checks what a task will do before it is stored, so the
// scheduler never holds a task it cannot run.
func validateTask(t *store.ScheduledTask) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > maxTaskNameLength {
		return badField("name", "name is required and must be at most %d characters", maxTaskNameLength)
	}
	t.CronExpr = strings.TrimSpace(t.CronExpr)
	if err := scheduler.ValidateCron(t.CronExpr); err != nil {
		return badField("cron_expr", "%s", err.Error())
	}
	if _, ok := store.RequiredTaskPermission(t.Action); !ok {
		return badField("action", "action must be one of command, start, restart, stop, backup or mod_update")
	}
	if t.Action == "command" {
		var p struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(t.Payload, &p); err != nil || strings.TrimSpace(p.Command) == "" {
			return badField("payload", "a command task needs a command")
		}
		if strings.ContainsAny(p.Command, "\r\n") || len(p.Command) > maxConsoleCommandLength {
			return badField("payload", "a command is one line of at most 1000 characters")
		}
	}
	return nil
}

func (h *TaskHandlers) Create(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	var body struct {
		Name     string          `json:"name"`
		CronExpr string          `json:"cron_expr"`
		Action   string          `json:"action"`
		Payload  json.RawMessage `json:"payload"`
		Enabled  bool            `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	creator := currentUserID(r)
	t := &store.ScheduledTask{
		ServerID:  serverID,
		Name:      body.Name,
		CronExpr:  body.CronExpr,
		Action:    body.Action,
		Payload:   body.Payload,
		Enabled:   body.Enabled,
		CreatedBy: &creator,
	}
	if err := validateTask(t); err != nil {
		respondInvalid(w, err)
		return
	}
	if status, msg := h.authorizeAction(r, serverID, t.Action); status != 0 {
		writeError(w, status, msg)
		return
	}

	created, err := h.store.CreateTask(r.Context(), t)
	if err != nil {
		writeServerError(w, r, "create task", err)
		return
	}
	audit(h.store, r, serverID, "task.create", map[string]any{
		"task_id": created.ID, "name": created.Name, "action": created.Action, "cron": created.CronExpr,
	})
	writeJSON(w, http.StatusCreated, created)
}

func (h *TaskHandlers) Update(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")

	existing, err := h.store.GetTask(r.Context(), taskID)
	if err != nil || existing.ServerID != serverID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	before := *existing

	var body struct {
		Name     *string         `json:"name"`
		CronExpr *string         `json:"cron_expr"`
		Action   *string         `json:"action"`
		Payload  json.RawMessage `json:"payload"`
		Enabled  *bool           `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.CronExpr != nil {
		existing.CronExpr = *body.CronExpr
	}
	if body.Action != nil {
		existing.Action = *body.Action
	}
	if body.Payload != nil {
		existing.Payload = body.Payload
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if err := validateTask(existing); err != nil {
		respondInvalid(w, err)
		return
	}

	// Changing what the task does (its action or payload) re-runs the permission
	// check against the resulting action, and re-attributes the task to the editor
	// so fire-time re-authorization tracks who set the current behavior.
	if body.Action != nil || body.Payload != nil {
		if status, msg := h.authorizeAction(r, serverID, existing.Action); status != 0 {
			writeError(w, status, msg)
			return
		}
		editor := currentUserID(r)
		existing.CreatedBy = &editor
	}

	if err := h.store.UpdateTask(r.Context(), taskID, existing); err != nil {
		writeServerError(w, r, "update task", err)
		return
	}
	changes := map[string]any{}
	if before.Name != existing.Name {
		changes["name"] = existing.Name
	}
	if before.CronExpr != existing.CronExpr {
		changes["cron"] = map[string]string{"from": before.CronExpr, "to": existing.CronExpr}
	}
	if before.Action != existing.Action {
		changes["action"] = map[string]string{"from": before.Action, "to": existing.Action}
	}
	if string(before.Payload) != string(existing.Payload) {
		changes["payload"] = "changed"
	}
	if before.Enabled != existing.Enabled {
		changes["enabled"] = existing.Enabled
	}
	if len(changes) > 0 {
		audit(h.store, r, serverID, "task.update", map[string]any{"task_id": taskID, "changes": changes})
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *TaskHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")
	existing, err := h.store.GetTask(r.Context(), taskID)
	if err != nil || existing.ServerID != serverID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err := h.store.DeleteTask(r.Context(), taskID); err != nil {
		writeServerError(w, r, "delete task", err)
		return
	}
	audit(h.store, r, serverID, "task.delete", map[string]any{"task_id": taskID, "name": existing.Name, "action": existing.Action})
	w.WriteHeader(http.StatusNoContent)
}

// Runs returns the run history: one task's when the route names a task, every
// task's on the server otherwise. ?limit caps the page.
func (h *TaskHandlers) Runs(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")
	if taskID != "" {
		t, err := h.store.GetTask(r.Context(), taskID)
		if err != nil || t.ServerID != serverID {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := h.store.ListTaskRuns(r.Context(), serverID, taskID, limit)
	if err != nil {
		writeServerError(w, r, "list task runs", err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
