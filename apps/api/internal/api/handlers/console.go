package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/store"
)

type ConsoleHandlers struct {
	store *store.Store
}

func NewConsoleHandlers(s *store.Store) *ConsoleHandlers {
	return &ConsoleHandlers{store: s}
}

func (h *ConsoleHandlers) Console(w http.ResponseWriter, r *http.Request) {
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
	ws.ProxyConsole(w, r, c, id)
}

func (h *ConsoleHandlers) Metrics(w http.ResponseWriter, r *http.Request) {
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
	ws.ProxyMetrics(w, r, c, id)
}
