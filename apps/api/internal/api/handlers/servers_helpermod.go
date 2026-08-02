package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
)

// helperModFileName must match apps/agent/internal/helperjar.FileName. It is
// duplicated rather than imported because the API and agent are separate
// modules; the mod-listing test guards the pair.
const helperModFileName = "mcsm-helper.jar"

// isManagerOwnedMod reports whether a jar in a server's mods directory is one
// the manager installs and owns, rather than something the user added.
func isManagerOwnedMod(fileName string) bool {
	name := strings.ToLower(strings.TrimSpace(fileName))
	// Also match the disabled form, so toggling a mod off in the panel does not
	// cause the helper to be adopted on the next reconcile.
	return name == helperModFileName || name == helperModFileName+disabledSuffix
}

// The helper-mod predicates live in the agent package alongside the start-config
// builder that consumes them, so the answer the UI shows and the answer the
// start payload carries cannot drift apart. Aliased here to keep call sites in
// this file readable.
const helperModMCSeries = agent.HelperModMCSeries

func helperModCompatible(platform, mcVersion string) bool {
	return agent.HelperModCompatible(platform, mcVersion)
}

func helperModEnabled(settings json.RawMessage, platform, mcVersion string) bool {
	return agent.HelperModEnabled(settings, platform, mcVersion)
}

// HelperModStatus reports whether the helper mod is enabled for a server.
//
// Also reports whether the platform can run it at all, so the UI can show the
// control as unavailable rather than offering a toggle that would do nothing.
func (h *ServerHandlers) HelperModStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil || srv == nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":            helperModEnabled(srv.Settings, srv.Platform, srv.MCVersion),
		"supported":          helperModCompatible(srv.Platform, srv.MCVersion),
		"platform":           srv.Platform,
		"mc_version":         srv.MCVersion,
		"required_mc_series": helperModMCSeries,
	})
}

// SetHelperMod turns the helper mod on or off for a server.
//
// The change is recorded but not applied to a running server: installing or
// removing a mod under a live JVM does nothing useful, because Fabric reads
// mods/ once at boot. The response says so explicitly rather than leaving the
// user to wonder why nothing happened.
func (h *ServerHandlers) SetHelperMod(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}

	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil || srv == nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	if *body.Enabled && !helperModCompatible(srv.Platform, srv.MCVersion) {
		if srv.Platform != "fabric" {
			writeError(w, http.StatusBadRequest, "the helper mod currently supports Fabric servers only")
			return
		}
		writeError(w, http.StatusBadRequest,
			"the helper mod requires Minecraft "+helperModMCSeries+"; this server runs "+srv.MCVersion)
		return
	}

	updated, err := setHelperModSetting(srv.Settings, *body.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update server settings")
		return
	}

	srv.Settings = updated
	if err := h.store.UpdateServer(r.Context(), id, srv); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save server settings")
		return
	}

	audit(h.store, r, id, "server.helper_mod", map[string]any{"enabled": *body.Enabled})

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": *body.Enabled,
		// Fabric reads mods/ once at boot, so the change lands on the next start.
		// Restart now sends the current configuration to the agent, so a plain
		// restart is genuinely enough — it did not used to be, which made this
		// flag a lie.
		"restart_required": true,
	})
}
