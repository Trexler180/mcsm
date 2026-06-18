package handlers

import (
	"encoding/json"
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

// Meta reports the server's Bedrock-bridge setup (Geyser/Floodgate install and
// the Bedrock username prefix), letting the UI show that Bedrock players are
// supported even when none are currently in the roster.
func (h *PlayersHandlers) Meta(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	info, err := h.mgr.GeyserInfo(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// Bans returns the server's consolidated ban state: player bans
// (banned-players.json) and IP bans (banned-ips.json) with their stored
// metadata. Read directly from disk so it works whether or not the server is
// running.
func (h *PlayersHandlers) Bans(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	bans, err := h.mgr.Bans(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bans)
}

// Action performs a player administration action (op/deop/ban/pardon/kick/
// ban_ip/pardon_ip/whitelist add or remove). When the server is online it is
// issued as a console command; when offline the relevant config file is edited
// directly.
func (h *PlayersHandlers) Action(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		UUID   string `json:"uuid"`
		Reason string `json:"reason"`
		// IP is the target address for the ban_ip / pardon_ip actions.
		IP string `json:"ip"`
		// Level is the desired operator permission level (1–4) for the op action;
		// 0 means "use the default". It only applies to offline ops.json edits —
		// a live server's /op command grants the server's default level.
		Level int `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.mgr.ApplyPlayerAction(id, body.Action, body.Name, body.UUID, body.Reason, body.IP, body.Level); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
