package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

type AuditHandlers struct {
	store *store.Store
}

func NewAuditHandlers(s *store.Store) *AuditHandlers {
	return &AuditHandlers{store: s}
}

// List returns recent audit entries across all servers (admin only).
func (h *AuditHandlers) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.store.ListAudit(r.Context(), "", limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// ListForServer returns recent audit entries for a single server.
func (h *AuditHandlers) ListForServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.store.ListAudit(r.Context(), serverID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
