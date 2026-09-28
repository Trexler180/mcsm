package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/auth"
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
	_, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}
	ws.ProxyConsole(w, r, c, id, h.permissionCheck(r, id, store.ServerPermissionConsole))
}

func (h *ConsoleHandlers) Metrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}
	ws.ProxyMetrics(w, r, c, id, h.permissionCheck(r, id, store.ServerPermissionView))
}

func (h *ConsoleHandlers) permissionCheck(r *http.Request, serverID string, permission store.ServerPermission) ws.PermissionCheck {
	claims := auth.ClaimsFrom(r.Context())
	// The route gate already checked an access key's allowlist and scopes, which
	// never change for a key. What can change while the socket is open is
	// whether the key still authenticates at all — revoked, rotated, expired, or
	// its owner gone — so that is re-checked alongside the owner's permission.
	machine := auth.MachineFrom(r.Context())
	return func(ctx context.Context) bool {
		if claims == nil {
			return false
		}
		if machine != nil && !h.store.AccessKeyStillValid(ctx, machine.KeyID, machine.TokenHash) {
			return false
		}
		user, err := h.store.GetUserByID(ctx, claims.UserID)
		if err != nil {
			return false
		}
		if user.Role == "admin" {
			return true
		}
		ok, err := h.store.UserHasServerPermission(ctx, claims.UserID, serverID, permission)
		return err == nil && ok
	}
}
