package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/notify"
)

const disabledSuffix = ".disabled"

// SetEnabled toggles whether a mod jar is loaded by the server. Disabling renames
// the file to "<name>.disabled" on the agent; enabling strips the suffix. The DB
// row's file_name is updated to match so uninstall/update keep working.
func (h *ModHandlers) SetEnabled(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil || mod.ServerID != serverID {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}
	// Already in the desired state: nothing to rename.
	if mod.Enabled == body.Enabled {
		writeJSON(w, http.StatusOK, mod)
		return
	}

	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
		return
	}

	newName := mod.FileName
	if body.Enabled {
		newName = strings.TrimSuffix(mod.FileName, disabledSuffix)
	} else if !strings.HasSuffix(mod.FileName, disabledSuffix) {
		newName = mod.FileName + disabledSuffix
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	if err := renameAgentFile(ctx, c, serverID, mod.InstallPath+"/"+mod.FileName, mod.InstallPath+"/"+newName); err != nil {
		writeError(w, http.StatusBadGateway, "agent rename failed: "+err.Error())
		return
	}

	if err := h.store.SetModEnabled(r.Context(), modID, body.Enabled, newName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "mod.disable"
	if body.Enabled {
		action = "mod.enable"
	}
	audit(h.store, r, serverID, action, map[string]any{"mod_id": modID, "name": mod.Name})
	mod.Enabled = body.Enabled
	mod.FileName = newName
	writeJSON(w, http.StatusOK, mod)
}

// DisableConflict applies a detected Fabric mod-conflict fix: it asks the agent
// to disable the jars matching the supplied loader mod ids, and syncs the
// enabled flag on any matching DB-tracked mods so the panel stays consistent.
func (h *ModHandlers) DisableConflict(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	var body struct {
		ModIDs []string `json:"mod_ids"`
	}
	if err := decode(r, &body); err != nil || len(body.ModIDs) == 0 {
		writeError(w, http.StatusBadRequest, "mod_ids required")
		return
	}

	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Make sure the agent knows the directory even if the instance was lost.
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	disabled, err := c.DisableConflictMods(ctx, serverID, body.ModIDs)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Best-effort: reflect the disable in the panel's mod list by matching the
	// renamed jar filenames to installed_mods rows.
	if mods, err := h.store.ListMods(r.Context(), serverID); err == nil {
		gone := map[string]bool{}
		for _, name := range disabled {
			gone[name] = true
		}
		for _, m := range mods {
			if m.Enabled && gone[m.FileName] {
				_ = h.store.SetModEnabled(r.Context(), m.ID, false, m.FileName+disabledSuffix)
			}
		}
	}

	// The offending jars are now disabled, so any open conflict for this server
	// is considered resolved.
	_ = h.store.ResolveServerConflicts(r.Context(), serverID)

	audit(h.store, r, serverID, "mod.disable_conflict", map[string]any{"mod_ids": body.ModIDs, "disabled": disabled})
	writeJSON(w, http.StatusOK, map[string]any{"disabled": disabled})
}

// ListConflicts returns persisted mod conflicts for a server. Pass ?active=1 to
// only return unresolved conflicts.
func (h *ModHandlers) ListConflicts(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	activeOnly := r.URL.Query().Get("active") == "1"
	conflicts, err := h.store.ListConflicts(r.Context(), serverID, activeOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conflicts)
}

// RecordConflict persists a conflict detected client-side from the console
// output, so the cockpit can surface unresolved conflicts across servers.
func (h *ModHandlers) RecordConflict(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	var body struct {
		Kind    string   `json:"kind"`
		Summary string   `json:"summary"`
		Mods    []string `json:"mods"`
	}
	if err := decode(r, &body); err != nil || body.Summary == "" {
		writeError(w, http.StatusBadRequest, "summary required")
		return
	}
	id, err := h.store.RecordConflict(r.Context(), serverID, body.Kind, body.Summary, body.Mods)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	serverName := ""
	if srv, err := h.store.GetServer(r.Context(), serverID); err == nil {
		serverName = srv.Name
	}
	h.notifier.Emit(notify.ModConflict(serverID, serverName, body.Summary))
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h *ModHandlers) Uninstall(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil || mod.ServerID != serverID {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}

	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	// A6: verify the agent actually removed the file before we forget about it.
	if err := deleteAgentFile(ctx, c, serverID, mod.InstallPath+"/"+mod.FileName); err != nil {
		writeError(w, http.StatusBadGateway, "agent delete failed: "+err.Error())
		return
	}

	if _, err := h.store.DeleteMod(r.Context(), modID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Drop this mod from the dependency graph so its required deps can become
	// orphaned (and any stale edges pointing at it are cleared).
	if mod.SourceID != nil {
		if err := h.store.DeleteModDependencyEdges(r.Context(), serverID, *mod.SourceID); err != nil {
			// Non-fatal: the mod is already gone; orphan flags will just be stale.
			audit(h.store, r, serverID, "mod.dep_cleanup_failed", map[string]any{"mod_id": modID, "error": err.Error()})
		}
	}
	audit(h.store, r, serverID, "mod.uninstall", map[string]any{"mod_id": modID, "name": mod.Name})
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ──────────────────────────────────────────────────────────

// verifyJarFile rejects a downloaded ".jar" that isn't a zip archive. Sources
// without hashes (CurseForge, SpigotMC) download through redirects that can
// land on an HTML page (login wall, error page) instead of the file; pushing
// that to the server would break the next boot.
