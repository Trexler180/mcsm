package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func requireServerAccess(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.ClaimsFrom(r.Context())
			if claims == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if claims.Role == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			serverID := chi.URLParam(r, "id")
			if serverID == "" {
				writeJSONError(w, http.StatusBadRequest, "server id required")
				return
			}

			ok, err := s.UserCanAccessServer(r.Context(), claims.UserID, serverID)
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
