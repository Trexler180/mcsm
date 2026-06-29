package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type TaskHandlers struct {
	store *store.Store
}

func NewTaskHandlers(s *store.Store) *TaskHandlers {
	return &TaskHandlers{store: s}
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*store.ScheduledTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
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
	if err := decode(r, &body); err != nil || body.Name == "" || body.CronExpr == "" || body.Action == "" {
		writeError(w, http.StatusBadRequest, "name, cron_expr, and action required")
		return
	}
	if status, msg := h.authorizeAction(r, serverID, body.Action); status != 0 {
		writeError(w, status, msg)
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

	created, err := h.store.CreateTask(r.Context(), t)
	if err != nil {
		writeServerError(w, r, "create task", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *TaskHandlers) Update(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")

	existing, err := h.store.GetTask(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if existing.ServerID != serverID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
