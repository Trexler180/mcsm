package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Vitals proxies the helper mod's live telemetry (TPS, MSPT, heap, counts)
// from the server's agent.
//
// A straight proxy rather than a reshape: the agent already answers with the
// UI-facing contract, and the data is served from the agent's memory, so this
// is cheap enough to poll at the dashboard's refresh cadence without touching
// the Minecraft server itself.
func (h *ServerHandlers) Vitals(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c.ProxyHTTP(ctx, w, r, "/agent/v1/servers/"+srv.ID+"/vitals")
}
