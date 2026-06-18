package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type ServerMemberHandlers struct {
	store *store.Store
}

func NewServerMemberHandlers(s *store.Store) *ServerMemberHandlers {
	return &ServerMemberHandlers{store: s}
}

type serverMembersResponse struct {
	Owner   *store.ServerMember   `json:"owner"`
	Members []*store.ServerMember `json:"members"`
}

type myServerPermissionsResponse struct {
	Owner       bool     `json:"owner"`
	GlobalAdmin bool     `json:"global_admin"`
	Permissions []string `json:"permissions"`
}

func ownerMember(serverID string, u *store.User) *store.ServerMember {
	return &store.ServerMember{
		ServerID:    serverID,
		UserID:      u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		Owner:       true,
		Permissions: store.AllServerPermissions(),
	}
}

func normalizeMemberPermissions(perms *[]string) ([]string, error) {
	if perms == nil {
		return nil, errors.New("permissions required")
	}
	normalized, err := store.NormalizeServerPermissions(*perms)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, errors.New("permissions required")
	}
	return normalized, nil
}

func (h *ServerMemberHandlers) server(r *http.Request) (*store.Server, error) {
	return h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
}

// serverAndOwner additionally resolves the owner's user record; only List needs
// it (to present the owner row), so the other handlers use server() instead.
func (h *ServerMemberHandlers) serverAndOwner(r *http.Request) (*store.Server, *store.User, error) {
	srv, err := h.server(r)
	if err != nil {
		return nil, nil, err
	}
	owner, err := h.store.GetUserByID(r.Context(), srv.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	return srv, owner, nil
}

func (h *ServerMemberHandlers) List(w http.ResponseWriter, r *http.Request) {
	srv, owner, err := h.serverAndOwner(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	members, err := h.store.ListServerMembers(r.Context(), srv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The owner has implicit full access and is returned separately, so drop any
	// stray owner row defensively. Build a fresh slice (never nil for the JSON).
	filtered := make([]*store.ServerMember, 0, len(members))
	for _, member := range members {
		if member.UserID != srv.OwnerID {
			filtered = append(filtered, member)
		}
	}
	writeJSON(w, http.StatusOK, serverMembersResponse{
		Owner:   ownerMember(srv.ID, owner),
		Members: filtered,
	})
}

func (h *ServerMemberHandlers) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	srv, err := h.server(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role == "admin" {
		writeJSON(w, http.StatusOK, myServerPermissionsResponse{
			GlobalAdmin: true,
			Permissions: store.AllServerPermissions(),
		})
		return
	}
	if srv.OwnerID == claims.UserID {
		writeJSON(w, http.StatusOK, myServerPermissionsResponse{
			Owner:       true,
			Permissions: store.AllServerPermissions(),
		})
		return
	}
	perms, ok, err := h.store.GetServerPermissions(r.Context(), srv.ID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, myServerPermissionsResponse{Permissions: perms})
}

func (h *ServerMemberHandlers) Create(w http.ResponseWriter, r *http.Request) {
	srv, err := h.server(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var body struct {
		UserID      string    `json:"user_id"`
		Email       string    `json:"email"`
		Permissions *[]string `json:"permissions"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	perms, err := normalizeMemberPermissions(body.Permissions)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid permissions")
		return
	}

	target, err := h.resolveTargetUser(r, body.UserID, body.Email)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found or cannot be added")
		return
	}
	if target.ID == srv.OwnerID {
		writeError(w, http.StatusBadRequest, "owner already has implicit access")
		return
	}
	if _, err := h.store.GetServerMember(r.Context(), srv.ID, target.ID); err == nil {
		writeError(w, http.StatusConflict, "user already has server access")
		return
	} else if !errors.Is(err, store.ErrServerMemberNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.store.SetServerPermissions(r.Context(), srv.ID, target.ID, perms); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, srv.ID, "server.member.add", map[string]any{
		"user_id":     target.ID,
		"email":       target.Email,
		"before":      []string{},
		"permissions": perms,
	})
	member, err := h.store.GetServerMember(r.Context(), srv.ID, target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

func (h *ServerMemberHandlers) Update(w http.ResponseWriter, r *http.Request) {
	srv, err := h.server(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	targetID := chi.URLParam(r, "userId")
	if targetID == srv.OwnerID {
		writeError(w, http.StatusBadRequest, "owner access cannot be changed")
		return
	}
	var body struct {
		Permissions         *[]string `json:"permissions"`
		ExpectedPermissions *[]string `json:"expected_permissions"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	perms, err := normalizeMemberPermissions(body.Permissions)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid permissions")
		return
	}
	if body.ExpectedPermissions == nil {
		writeError(w, http.StatusBadRequest, "expected_permissions required")
		return
	}
	existing, err := h.store.GetServerMember(r.Context(), srv.ID, targetID)
	if errors.Is(err, store.ErrServerMemberNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.store.SetServerPermissionsIfCurrent(r.Context(), srv.ID, targetID, perms, *body.ExpectedPermissions); err != nil {
		if errors.Is(err, store.ErrServerPermissionsStale) {
			writeError(w, http.StatusConflict, "member permissions changed; refresh and retry")
			return
		}
		if errors.Is(err, store.ErrServerMemberNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, srv.ID, "server.member.update", map[string]any{
		"user_id": targetID,
		"email":   existing.Email,
		"before":  existing.Permissions,
		"after":   perms,
	})
	updated, err := h.store.GetServerMember(r.Context(), srv.ID, targetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *ServerMemberHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	srv, err := h.server(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	targetID := chi.URLParam(r, "userId")
	if targetID == srv.OwnerID {
		writeError(w, http.StatusBadRequest, "owner access cannot be changed")
		return
	}
	existing, err := h.store.GetServerMember(r.Context(), srv.ID, targetID)
	if errors.Is(err, store.ErrServerMemberNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.store.DeleteServerPermissions(r.Context(), srv.ID, targetID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, srv.ID, "server.member.remove", map[string]any{
		"user_id": targetID,
		"email":   existing.Email,
		"before":  existing.Permissions,
		"after":   []string{},
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *ServerMemberHandlers) resolveTargetUser(r *http.Request, userID, email string) (*store.User, error) {
	if userID != "" {
		return h.store.GetUserByID(r.Context(), userID)
	}
	return h.store.GetUserByEmailInsensitive(r.Context(), email)
}
