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

// List returns the merged player roster for a server: those currently online
// (tracked live from the console) plus offline players read from the world's
// playerdata files. Returns an empty array (not an error) when there's nothing
// to show so the UI can render cleanly.
func (h *PlayersHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	players := h.mgr.AllPlayers(id)
	if players == nil {
		players = []process.Player{}
	}
	writeJSON(w, http.StatusOK, players)
}

// Detail parses and returns a single player's saved data (inventory, ender
// chest, health, position, etc.) from their world .dat file.
func (h *PlayersHandlers) Detail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	uuid := chi.URLParam(r, "uuid")
	if !process.ValidUUID(uuid) {
		writeError(w, http.StatusBadRequest, "invalid player uuid")
		return
	}
	d, err := h.mgr.PlayerDetail(id, uuid)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}
