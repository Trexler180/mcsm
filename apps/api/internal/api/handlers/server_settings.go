package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// A server's settings as typed fields rather than a raw JSON blob: what each
// one is, its current value, and whether this caller may change it. Changes go
// through the same per-key validation as a full server update.

type settingsField struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Editable    bool   `json:"editable"`
}

type typedSettings struct {
	HelperMod      *bool               `json:"helper_mod"`
	ResourcePack   *resourcePackFields `json:"resource_pack"`
	ConnectAddress string              `json:"connect_address"`
	Import         json.RawMessage     `json:"import,omitempty"`
}

var settingsCatalog = []settingsField{
	{Key: "helper_mod", Type: "boolean", Label: "Helper mod",
		Description: "Install the panel's helper mod on start, for live vitals, the player list and faster commands."},
	{Key: "resource_pack", Type: "object", Label: "Resource pack",
		Description: "A pack players are offered when they join, served by the panel."},
	{Key: "connect_address", Type: "string", Label: "Connect address",
		Description: "The address players type to join. Empty means the node's address and the server's port."},
	{Key: "import", Type: "object", Label: "Imported server",
		Description: "Which jar an imported server runs, and whether the panel installs a runtime. Administrators only."},
}

func (h *ServerHandlers) settingsView(raw json.RawMessage, isAdmin bool) map[string]any {
	var values typedSettings
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	fields := make([]settingsField, len(settingsCatalog))
	for i, f := range settingsCatalog {
		f.Editable = settingsKeys[f.Key] || isAdmin
		fields[i] = f
	}
	if !isAdmin {
		// What an imported server executes is the admin's business.
		values.Import = nil
	}
	return map[string]any{"values": values, "fields": fields}
}

// Settings answers GET /servers/{id}/settings.
func (h *ServerHandlers) Settings(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	writeJSON(w, http.StatusOK, h.settingsView(srv.Settings, h.isAdmin(r)))
}

// PatchSettings answers PATCH /servers/{id}/settings: the keys sent replace
// those keys (null removes one), everything else is kept.
func (h *ServerHandlers) PatchSettings(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	var patch map[string]json.RawMessage
	if err := decode(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "settings patch must be an object")
		return
	}
	current := map[string]json.RawMessage{}
	if len(srv.Settings) > 0 {
		_ = json.Unmarshal(srv.Settings, &current)
	}
	next := map[string]json.RawMessage{}
	for k, v := range current {
		next[k] = v
	}
	for k, v := range patch {
		if string(v) == "null" {
			delete(next, k)
			continue
		}
		next[k] = v
	}
	nextRaw, _ := json.Marshal(next)
	isAdmin := h.isAdmin(r)
	// A non-admin may not remove an admin-only key by sending null either.
	if !isAdmin {
		for k := range patch {
			if editable, known := settingsKeys[k]; known && !editable {
				writeFieldError(w, "settings", "only an administrator may change "+k)
				return
			}
		}
	}
	validated, err := validateSettingsChange(srv.Settings, nextRaw, isAdmin)
	if err != nil {
		respondInvalid(w, err)
		return
	}
	srv.Settings = validated
	if err := h.store.UpdateServer(r.Context(), srv.ID, srv); err != nil {
		writeServerError(w, r, "patch settings", err)
		return
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	audit(h.store, r, srv.ID, "server.settings", map[string]any{"keys": keys})
	writeJSON(w, http.StatusOK, h.settingsView(validated, isAdmin))
}
