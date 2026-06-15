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

// proxy registers the server directory with its agent (so playerdata files can
// be read even while the server is offline) and forwards the request to the
// given agent path.
func (h *PlayersHandlers) proxy(w http.ResponseWriter, r *http.Request, agentSuffix string) {
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
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	c.ProxyHTTP(ctx, w, r, "/agent/v1/servers/"+srv.ID+agentSuffix)
}

// List proxies the merged player roster (online + offline-from-world-files).
func (h *PlayersHandlers) List(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players")
}

// Detail proxies a single player's parsed .dat data (inventory, stats, etc.).
func (h *PlayersHandlers) Detail(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/"+chi.URLParam(r, "uuid"))
}

// Meta proxies the server's Bedrock-bridge info (Geyser/Floodgate install +
// username prefix).
func (h *PlayersHandlers) Meta(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/meta")
}

// Action proxies a player administration action (op/ban/whitelist/etc.). The
// agent applies it live via console when the server is up, or by editing the
// config files directly when it is down.
func (h *PlayersHandlers) Action(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/action")
}
