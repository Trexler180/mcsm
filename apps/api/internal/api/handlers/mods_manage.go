package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
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
		// Force acknowledges that other content requires this mod; without it,
		// disabling something depended on is refused with the impact report.
		Force bool `json:"force"`
		// DisableDependents turns the mods that require this one off as well, so
		// the server boots with a consistent set instead of failing on the
		// missing dependency.
		DisableDependents bool `json:"disable_dependents"`
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

	// Disabling a jar removes it from the loader's view just as surely as
	// deleting it, so it goes through the same dependency guard. Enabling can
	// only ever satisfy dependencies, never break them.
	var impact *ModImpact
	if !body.Enabled {
		var ok bool
		if impact, ok = h.checkDependencyImpact(w, r, serverID, mod, body.Force, "disabling"); !ok {
			return
		}
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
		writeServerError(w, r, "mod set enabled", err)
		return
	}

	// Take the dependents down with it when asked. The mod itself is already
	// disabled at this point, so a partial result here is still better than
	// leaving dependents loaded against a dependency that is gone.
	var alsoDisabled, failedToDisable []string
	if !body.Enabled && body.DisableDependents && impact != nil && len(impact.Required) > 0 {
		depCtx, depCancel := context.WithTimeout(r.Context(), 60*time.Second)
		alsoDisabled, failedToDisable = h.disableDependents(depCtx, c, serverID, impact.Required)
		depCancel()
	}

	action := "mod.disable"
	if body.Enabled {
		action = "mod.enable"
	}
	entry := map[string]any{"mod_id": modID, "name": mod.Name}
	if len(alsoDisabled) > 0 {
		entry["dependents_disabled"] = alsoDisabled
	}
	if len(failedToDisable) > 0 {
		entry["dependents_disable_failed"] = failedToDisable
	}
	audit(h.store, r, serverID, action, entry)
	mod.Enabled = body.Enabled
	mod.FileName = newName

	// The mod's own fields stay at the top level (the response shape callers
	// already parse); what happened to its dependents rides alongside.
	writeJSON(w, http.StatusOK, struct {
		*store.InstalledMod
		DependentsDisabled []string `json:"dependents_disabled,omitempty"`
		DependentsFailed   []string `json:"dependents_failed,omitempty"`
	}{mod, alsoDisabled, failedToDisable})
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
		writeServerError(w, r, "list conflicts", err)
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
		writeServerError(w, r, "record conflict", err)
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

	// Refuse to delete something other content requires unless the caller says
	// it accepts that, and offer to take the dependents offline with it.
	force := boolQuery(r, "force")
	impact, ok := h.checkDependencyImpact(w, r, serverID, mod, force, "removing")
	if !ok {
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
		writeServerError(w, r, "uninstall mod: record", err)
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

	// Optionally take the now-broken dependents offline in the same operation,
	// so the next boot doesn't fail on a dependency that no longer exists. Their
	// jars stay on disk, so re-installing the dependency and re-enabling them
	// restores the previous state.
	withDependents := boolQuery(r, "disable_dependents")
	var alsoDisabled, failedToDisable []string
	if withDependents && len(impact.Required) > 0 {
		depCtx, depCancel := context.WithTimeout(r.Context(), 60*time.Second)
		alsoDisabled, failedToDisable = h.disableDependents(depCtx, c, serverID, impact.Required)
		depCancel()
	}

	entry := map[string]any{"mod_id": modID, "name": mod.Name}
	if len(impact.Required) > 0 {
		entry["broke_dependents"] = len(impact.Required)
	}
	if len(alsoDisabled) > 0 {
		entry["dependents_disabled"] = alsoDisabled
	}
	if len(failedToDisable) > 0 {
		entry["dependents_disable_failed"] = failedToDisable
	}
	audit(h.store, r, serverID, "mod.uninstall", entry)

	// 204 keeps the plain delete unchanged; a delete that was asked to take
	// dependents with it has something to report, so it answers with what it did.
	if withDependents {
		writeJSON(w, http.StatusOK, map[string]any{
			"disabled": nonNil(alsoDisabled),
			"failed":   nonNil(failedToDisable),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// boolQuery reads a truthy query flag ("1", "true", "yes").
func boolQuery(r *http.Request, name string) bool {
	switch strings.ToLower(r.URL.Query().Get(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// nonNil renders an empty list as [] rather than null, so clients can index it
// without a null check.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ── helpers ──────────────────────────────────────────────────────────

// verifyJarFile rejects a downloaded ".jar" that isn't a zip archive. Sources
// without hashes (CurseForge, SpigotMC) download through redirects that can
// land on an HTML page (login wall, error page) instead of the file; pushing
// that to the server would break the next boot.
