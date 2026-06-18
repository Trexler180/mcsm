package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func requireServerAccess(s *store.Store) func(http.Handler) http.Handler {
	return requireServerPermission(s, store.ServerPermissionView)
}

// requireServerPermission gates a route on a specific permission (group or
// leaf), satisfied by the leaf, its parent group, or admin.
func requireServerPermission(s *store.Store, permission store.ServerPermission) func(http.Handler) http.Handler {
	return serverAccessGate(s, func(r *http.Request, userID, serverID string) (bool, error) {
		return s.UserHasServerPermission(r.Context(), userID, serverID, permission)
	})
}

// requireServerGroupAccess gates a read/list route on any access within a
// group — holding the group or any of its leaves is enough.
func requireServerGroupAccess(s *store.Store, group store.ServerPermission) func(http.Handler) http.Handler {
	return serverAccessGate(s, func(r *http.Request, userID, serverID string) (bool, error) {
		return s.UserHasServerGroupAccess(r.Context(), userID, serverID, group)
	})
}

// serverAccessGate centralizes the claims/admin-bypass/server-id boilerplate so
// each permission middleware only supplies the access predicate. The global
// admin role is read fresh from the DB so a demotion takes effect immediately.
func serverAccessGate(s *store.Store, allow func(r *http.Request, userID, serverID string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.ClaimsFrom(r.Context())
			if claims == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			user, err := s.GetUserByID(r.Context(), claims.UserID)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if user.Role == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			serverID := chi.URLParam(r, "id")
			if serverID == "" {
				writeJSONError(w, http.StatusBadRequest, "server id required")
				return
			}

			ok, err := allow(r, claims.UserID, serverID)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "authorization check failed")
				return
			}
			if !ok {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
