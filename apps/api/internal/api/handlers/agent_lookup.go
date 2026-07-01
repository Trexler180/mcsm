package handlers

import (
	"net/http"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

// serverAgent resolves a server row and an agent client for its node, writing
// the 404 response itself when either lookup fails. Callers check ok and
// return on false. This is the shared prologue of every handler that talks to
// a server's agent.
func serverAgent(w http.ResponseWriter, r *http.Request, s *store.Store, serverID string) (*store.Server, *agent.Client, bool) {
	srv, err := s.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return nil, nil, false
	}
	node, err := s.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return nil, nil, false
	}
	return srv, agent.New(node.Scheme, node.FQDN, node.Port, node.Token), true
}
