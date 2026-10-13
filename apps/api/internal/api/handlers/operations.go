package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// OperationHandlers expose a server's operations — starts, backups, restores,
// migrations, safe updates — so the dashboard can follow one to its end.
type OperationHandlers struct {
	store *store.Store
	ops   *operations.Manager
}

func NewOperationHandlers(s *store.Store, ops *operations.Manager) *OperationHandlers {
	return &OperationHandlers{store: s, ops: ops}
}

// List returns a server's operations, newest first. ?before=<unix seconds>
// pages back; ?limit caps the page.
func (h *OperationHandlers) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	ops, err := h.store.ListOperations(r.Context(), chi.URLParam(r, "id"), limit, before)
	if err != nil {
		writeServerError(w, r, "list operations", err)
		return
	}
	current, _ := h.store.RunningOperation(r.Context(), chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"operations": ops, "current": current})
}

func (h *OperationHandlers) Get(w http.ResponseWriter, r *http.Request) {
	op, err := h.store.GetOperation(r.Context(), chi.URLParam(r, "opId"))
	if err != nil || op.ServerID != chi.URLParam(r, "id") {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// Cancel asks a running operation to stop. What stopping means is up to the
// operation: a safe update that is cancelled reverts what it applied and
// blocklists nothing; a backup that is cancelled is discarded.
func (h *OperationHandlers) Cancel(w http.ResponseWriter, r *http.Request) {
	op, err := h.store.GetOperation(r.Context(), chi.URLParam(r, "opId"))
	if err != nil || op.ServerID != chi.URLParam(r, "id") {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	if op.Status != store.OpRunning {
		writeCodedError(w, http.StatusConflict, "conflict", "this operation has already finished")
		return
	}
	if !h.ops.Cancel(op.ID) {
		writeCodedError(w, http.StatusConflict, "conflict", "this operation is not running in this process")
		return
	}
	audit(h.store, r, op.ServerID, "operation.cancelled", map[string]any{"operation_id": op.ID, "kind": op.Kind})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}
