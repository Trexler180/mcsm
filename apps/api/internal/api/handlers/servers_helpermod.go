package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
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

// helperModMCSeries mirrors the agent's constant and the jar's own
// fabric.mod.json. All three must move together.
const helperModMCSeries = "26.2"

// helperModCompatible reports whether the shipped jar can load on a server.
//
// Fabric Loader treats an unsatisfied dependency as fatal, so enabling the mod
// on the wrong Minecraft version would prevent that server from starting at
// all. The agent enforces this too; checking here as well is what lets the UI
// explain why the option is unavailable instead of accepting a toggle that
// silently does nothing.
func helperModCompatible(platform, mcVersion string) bool {
	if platform != "fabric" {
		return false
	}
	v := strings.TrimSpace(mcVersion)
	return v == helperModMCSeries || strings.HasPrefix(v, helperModMCSeries+".")
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
