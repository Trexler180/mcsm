package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// FolderHandlers manages server folders — the flat grouping shown above the
// server list. Reads are scoped to what the caller can see; writes are
// admin-only (registered that way in the router), matching server creation.
type FolderHandlers struct {
	store *store.Store
}

func NewFolderHandlers(s *store.Store) *FolderHandlers {
	return &FolderHandlers{store: s}
}

const (
	maxFolderNameLen        = 60
	maxFolderDescriptionLen = 200
)

// folderColors is the palette the UI offers. Restricting the field to known
// tokens (rather than free-form CSS) keeps a stored value from reaching the
// browser as an arbitrary style.
var folderColors = map[string]bool{
	"":       true, // default surface styling
	"slate":  true,
	"blue":   true,
	"green":  true,
	"amber":  true,
	"red":    true,
	"purple": true,
	"pink":   true,
	"cyan":   true,
}

// normalizeFolderInput validates and cleans the shared create/update fields.
func normalizeFolderInput(name, description, color string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", "", errors.New("folder name is required")
	}
	if len(name) > maxFolderNameLen {
		return "", "", "", errors.New("folder name is too long")
	}
	description = strings.TrimSpace(description)
	if len(description) > maxFolderDescriptionLen {
		return "", "", "", errors.New("folder description is too long")
	}
	color = strings.TrimSpace(strings.ToLower(color))
	if !folderColors[color] {
		return "", "", "", errors.New("unknown folder color")
	}
	return name, description, color, nil
}

func (h *FolderHandlers) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var (
		folders []*store.ServerFolder
		err     error
	)
	if claims.Role == "admin" {
		folders, err = h.store.ListServerFolders(r.Context())
	} else {
		// Non-admins never learn about folders holding nothing they can open.
		folders, err = h.store.ListServerFoldersForUser(r.Context(), claims.UserID)
	}
	if err != nil {
		writeServerError(w, r, "list server folders", err)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (h *FolderHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
		SortOrder   int    `json:"sort_order"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name, description, color, err := normalizeFolderInput(body.Name, body.Description, body.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := h.store.CreateServerFolder(r.Context(), &store.ServerFolder{
		Name:        name,
		Description: description,
		Color:       color,
		SortOrder:   body.SortOrder,
	})
	if errors.Is(err, store.ErrFolderNameTaken) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeServerError(w, r, "create server folder", err)
		return
	}

	audit(h.store, r, "", "folder.create", map[string]any{"folder_id": created.ID, "name": created.Name})
	writeJSON(w, http.StatusCreated, created)
}

func (h *FolderHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetServerFolder(r.Context(), id)
	if errors.Is(err, store.ErrFolderNotFound) {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	if err != nil {
		writeServerError(w, r, "get server folder", err)
		return
	}

	body := struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
		SortOrder   *int    `json:"sort_order"`
	}{}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Absent fields keep their current value, so a partial PUT is a patch.
	name, description, color := existing.Name, existing.Description, existing.Color
	if body.Name != nil {
		name = *body.Name
	}
	if body.Description != nil {
		description = *body.Description
	}
	if body.Color != nil {
		color = *body.Color
	}
	name, description, color, err = normalizeFolderInput(name, description, color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	existing.Name, existing.Description, existing.Color = name, description, color

	err = h.store.UpdateServerFolder(r.Context(), id, existing)
	if errors.Is(err, store.ErrFolderNameTaken) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrFolderNotFound) {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	if err != nil {
		writeServerError(w, r, "update server folder", err)
		return
	}

	updated, err := h.store.GetServerFolder(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "get updated server folder", err)
		return
	}
	audit(h.store, r, "", "folder.update", map[string]any{"folder_id": id, "name": updated.Name})
	writeJSON(w, http.StatusOK, updated)
}

// Delete removes the folder. Servers inside it are untouched — they simply
// become ungrouped — so this is never a destructive operation on a server.
func (h *FolderHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	folder, err := h.store.GetServerFolder(r.Context(), id)
	if errors.Is(err, store.ErrFolderNotFound) {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	if err != nil {
		writeServerError(w, r, "get server folder", err)
		return
	}

	if err := h.store.DeleteServerFolder(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeServerError(w, r, "delete server folder", err)
		return
	}

	audit(h.store, r, "", "folder.delete", map[string]any{
		"folder_id": id, "name": folder.Name, "servers_ungrouped": folder.ServerCount,
	})
	w.WriteHeader(http.StatusNoContent)
}
