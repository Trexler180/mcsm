package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

type PlayersHandlers struct {
	mgr *process.Manager
}

func NewPlayersHandlers(mgr *process.Manager) *PlayersHandlers {
	return &PlayersHandlers{mgr: mgr}
}

// List returns currently-online players for a server. Returns an empty array
// (not an error) for offline servers so the UI can render cleanly.
func (h *PlayersHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	players := h.mgr.Players(id)
	if players == nil {
		players = []process.Player{}
	}
	writeJSON(w, http.StatusOK, players)
}
