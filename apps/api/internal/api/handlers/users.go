package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

var validRoles = map[string]bool{"admin": true, "operator": true, "user": true}

type UserHandlers struct {
	store     *store.Store
	jwtSecret string
}

func NewUserHandlers(s *store.Store, jwtSecret string) *UserHandlers {
	return &UserHandlers{store: s, jwtSecret: jwtSecret}
}

func (h *UserHandlers) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []*store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *UserHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(r, &body); err != nil || body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}
	if body.Role == "" {
		body.Role = "user"
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "password hashing failed")
		return
	}

	user, err := h.store.CreateUser(r.Context(), body.Email, hash, body.Role)
	if err != nil {
		writeError(w, http.StatusConflict, "user already exists or db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *UserHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	var body struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Password    string  `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	role := existing.Role
	if body.Role != nil && *body.Role != "" {
		if !validRoles[*body.Role] {
			writeError(w, http.StatusBadRequest, "role must be admin, operator, or user")
			return
		}
		role = *body.Role
	}

	// Guard against an admin locking themselves out by self-demotion.
	if claims := auth.ClaimsFrom(r.Context()); claims != nil && claims.UserID == id && role != "admin" {
		writeError(w, http.StatusBadRequest, "you cannot change your own admin role")
		return
	}

	displayName := existing.DisplayName
	if body.DisplayName != nil {
		trimmed := strings.TrimSpace(*body.DisplayName)
		if trimmed == "" {
			displayName = nil
		} else {
			displayName = &trimmed
		}
	}

	if err := h.store.UpdateUser(r.Context(), id, displayName, role); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if body.Password != "" {
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "password hashing failed")
			return
		}
		if err := h.store.UpdateUserPassword(r.Context(), id, hash); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	updated, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *UserHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
