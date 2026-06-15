package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

// errCurseForgeKeyRejected is surfaced (as a 400) when CurseForge authoritatively
// refuses a key during validation.
var errCurseForgeKeyRejected = errors.New("key rejected by CurseForge (check the value and try again)")

// curseforgeVersionURL is the cheap authenticated endpoint used to validate a
// CurseForge key. A package var so tests can point it at a stub.
var curseforgeVersionURL = "https://api.curseforge.com/v1/minecraft/version"

type SettingsHandlers struct {
	store *store.Store
}

func NewSettingsHandlers(s *store.Store) *SettingsHandlers {
	return &SettingsHandlers{store: s}
}

// integrationDef describes a secret the UI knows how to manage. The allowlist
// keeps SetIntegration from being used to write arbitrary keys, and supplies
// the human-facing copy so the frontend stays dumb.
type integrationDef struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	DocURL      string `json:"doc_url"`
}

// knownIntegrations is the allowlist of manageable app secrets. Add new
// integration keys here; everything else is rejected by SetIntegration.
var knownIntegrations = []integrationDef{
	{
		Key:         "curseforge_api_key",
		Label:       "CurseForge API Key",
		Description: "Enables CurseForge mod search and categories. Get a key from the CurseForge developer console and paste it here.",
		DocURL:      "https://console.curseforge.com/",
	},
}

func integrationByKey(key string) (integrationDef, bool) {
	for _, d := range knownIntegrations {
		if d.Key == key {
			return d, true
		}
	}
	return integrationDef{}, false
}

// integrationStatus is one row in the ListIntegrations response: the static
// definition merged with the stored secret's metadata. Value is never included.
type integrationStatus struct {
	integrationDef
	Configured bool       `json:"configured"`
	Hint       string     `json:"hint"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

func (h *SettingsHandlers) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	meta, err := h.store.ListSecretMeta(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byKey := make(map[string]store.SecretMeta, len(meta))
	for _, m := range meta {
		byKey[m.Key] = m
	}

	out := make([]integrationStatus, 0, len(knownIntegrations))
	for _, def := range knownIntegrations {
		row := integrationStatus{integrationDef: def}
		if m, ok := byKey[def.Key]; ok {
			row.Configured = true
			row.Hint = m.Hint
			t := m.UpdatedAt
			row.UpdatedAt = &t
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *SettingsHandlers) SetIntegration(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	def, ok := integrationByKey(key)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown integration")
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	value := strings.TrimSpace(body.Value)
	if value == "" {
		writeError(w, http.StatusBadRequest, "value is required (use DELETE to clear a key)")
		return
	}

	// Best-effort validation for keys we know how to check. A network failure
	// must not block saving (the user may be offline-firewalled to the API
	// host while the server itself can reach it later), but an authoritative
	// rejection should.
	if def.Key == "curseforge_api_key" {
		if err := validateCurseForgeKey(r.Context(), value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := h.store.SetSecret(r.Context(), def.Key, value, currentUserID(r)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, "", "integration.set", map[string]string{"key": def.Key})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *SettingsHandlers) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if _, ok := integrationByKey(key); !ok {
		writeError(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err := h.store.DeleteSecret(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, "", "integration.delete", map[string]string{"key": key})
	w.WriteHeader(http.StatusNoContent)
}

// validateCurseForgeKey makes a cheap authenticated call to confirm the key is
// accepted. A 401/403 is an authoritative rejection; any other error (network,
// timeout, 5xx) is treated as "can't tell" and allows the save to proceed.
func validateCurseForgeKey(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, curseforgeVersionURL, nil)
	if err != nil {
		return nil // can't even build the probe; don't block the save
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil // network failure: save anyway
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errCurseForgeKeyRejected
	}
	return nil
}
