package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

type PlayersHandlers struct {
	store *store.Store
}

func NewPlayersHandlers(s *store.Store) *PlayersHandlers {
	return &PlayersHandlers{store: s}
}

// List proxies the agent's online-players list. The agent tracks players by
// watching the server console for "X joined the game" / "X left the game".
func (h *PlayersHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c.ProxyHTTP(ctx, w, r, "/agent/v1/servers/"+id+"/players")
}
