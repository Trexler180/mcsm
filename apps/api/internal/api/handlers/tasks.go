package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

type TaskHandlers struct {
	store *store.Store
}

func NewTaskHandlers(s *store.Store) *TaskHandlers {
	return &TaskHandlers{store: s}
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

	t := &store.ScheduledTask{
		ServerID: serverID,
		Name:     body.Name,
		CronExpr: body.CronExpr,
		Action:   body.Action,
		Payload:  body.Payload,
		Enabled:  body.Enabled,
	}

	created, err := h.store.CreateTask(r.Context(), t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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

	if err := h.store.UpdateTask(r.Context(), taskID, existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
