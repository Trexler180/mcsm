package handlers

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

func (h *ModHandlers) Updates(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	mods, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Blocklisted versions (auto-reverted by a previous run) are never offered.
	skippedRows, err := h.store.ListSkippedModVersions(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	skipped := map[string]map[string]bool{} // project id -> version id -> true
	for _, sv := range skippedRows {
		if skipped[sv.ProjectID] == nil {
			skipped[sv.ProjectID] = map[string]bool{}
		}
		skipped[sv.ProjectID][sv.VersionID] = true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	loader := modrinth.LoaderForPlatform(srv.Platform)
	type updateInfo struct {
		ModID           string `json:"mod_id"`
		Name            string `json:"name"`
		Source          string `json:"source"`
		Enabled         bool   `json:"enabled"`
		CurrentVersion  string `json:"current_version"`
		LatestVersion   string `json:"latest_version"`
		LatestVersionID string `json:"latest_version_id"`
	}
	// Update checks need GetVersions to return reliably-ordered, resolvable
	// versions; CurseForge's key-less listing is paged/filtered enough that it
	// stays out, matching its previous behavior.
	updatable := map[string]bool{"modrinth": true, "hangar": true, "spigotmc": true}
	out := []updateInfo{}
	for _, m := range mods {
		if !updatable[m.Source] || m.SourceID == nil || m.VersionID == nil || m.Pinned {
			continue
		}
		versions, err := h.sourceFor(m.Source).GetVersions(ctx, *m.SourceID, loader, srv.MCVersion)
		if err != nil || len(versions) == 0 {
			continue
		}
		target := autoupdate.PickUpdate(versions, *m.VersionID, skipped[*m.SourceID])
		if target != nil {
			out = append(out, updateInfo{
				ModID:           m.ID,
				Name:            m.Name,
				Source:          m.Source,
				Enabled:         m.Enabled,
				CurrentVersion:  m.Version,
				LatestVersion:   target.VersionNumber,
				LatestVersionID: target.ID,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Compatibility buckets returned by VersionCheck.
const (
	compatCompatible   = "compatible"   // a newer/other build exists for the target → would be swapped in
	compatSupported    = "supported"    // the installed build already lists the target version → no change
	compatIncompatible = "incompatible" // no build for the target → would be auto-disabled
	compatUnmanaged    = "unmanaged"    // custom jar or a source we can't reliably check → left untouched
	compatUnknown      = "unknown"      // the upstream lookup failed this run → review manually
)

type modCompat struct {
	ModID           string `json:"mod_id"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	CurrentVersion  string `json:"current_version"`
	Status          string `json:"status"`
	TargetVersion   string `json:"target_version,omitempty"`
	TargetVersionID string `json:"target_version_id,omitempty"`
	Pinned          bool   `json:"pinned"`
	Enabled         bool   `json:"enabled"`
	// DepWarnings names this mod's required dependencies that the migration would
	// disable (they have no build for the target), so a mod that itself migrates
	// fine may still not load. Advisory only — populated from the panel's
	// dependency graph, so it covers panel-installed deps, not custom jars.
	DepWarnings []string `json:"dep_warnings,omitempty"`
}

type versionCheckResult struct {
	MCVersion string         `json:"mc_version"`
	Loader    string         `json:"loader"`
	Total     int            `json:"total"`
	Counts    map[string]int `json:"counts"`
	Mods      []modCompat    `json:"mods"`
}

// versionCheckConcurrency bounds the per-mod upstream calls so a large modpack
// doesn't open one connection per mod at once.
const versionCheckConcurrency = 8

// VersionCheck previews how the installed mods would fare if the server moved to
// a different Minecraft version (upgrade or downgrade): for the target version it
// buckets each mod into compatible / already-supported / incompatible / unmanaged.
// Read-only — it changes nothing. GET /servers/{id}/mods/version-check?mc_version=X
func (h *ModHandlers) VersionCheck(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	target := strings.TrimSpace(r.URL.Query().Get("mc_version"))
	if target == "" {
		writeError(w, http.StatusBadRequest, "mc_version required")
		return
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	mods, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Soft-validate the target against the platform's known versions: reject an
	// obvious typo, but if the upstream list is unavailable, proceed anyway
	// rather than block the preview on a metadata hiccup.
	if known, err := h.mc.GameVersions(ctx, srv.Platform, true); err == nil {
		valid := false
		for _, v := range known {
			if v.Version == target {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, http.StatusBadRequest, "unknown Minecraft version for this platform: "+target)
			return
		}
	}

	loader := modrinth.LoaderForPlatform(srv.Platform)
	// Sources whose version listing is reliable enough to classify against; this
	// mirrors the update checker (mods.go Updates): CurseForge and custom jars are
	// surfaced as unmanaged so the operator reviews them by hand.
	checkable := map[string]bool{"modrinth": true, "hangar": true, "spigotmc": true}

	results := make([]modCompat, len(mods))
	sem := make(chan struct{}, versionCheckConcurrency)
	var wg sync.WaitGroup
	for i, m := range mods {
		base := modCompat{
			ModID:          m.ID,
			Name:           m.Name,
			Source:         m.Source,
			CurrentVersion: m.Version,
			Pinned:         m.Pinned,
			Enabled:        m.Enabled,
		}
		if !checkable[m.Source] || m.SourceID == nil || m.VersionID == nil {
			base.Status = compatUnmanaged
			results[i] = base
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m *store.InstalledMod, base modCompat) {
			defer wg.Done()
			defer func() { <-sem }()
			base.Status = classifyForTarget(ctx, h.sourceFor(m.Source), *m.SourceID, *m.VersionID, loader, target, &base)
			results[i] = base
		}(i, m, base)
	}
	wg.Wait()

	annotateDepWarnings(r.Context(), h.store, serverID, mods, results)

	counts := map[string]int{}
	for _, m := range results {
		counts[m.Status]++
	}
	writeJSON(w, http.StatusOK, versionCheckResult{
		MCVersion: target,
		Loader:    loader,
		Total:     len(results),
		Counts:    counts,
		Mods:      results,
	})
}

// annotateDepWarnings flags mods that would migrate fine on their own but whose
// required dependency the migration would disable (the dep has no build for the
// target), so the operator sees the broken link before applying. It uses the
// panel's dependency graph, so it only knows about panel-installed dependencies;
// custom jars contribute no edges and simply produce no warning. Best-effort:
// graph lookup failures leave the warnings empty rather than failing the preview.
func annotateDepWarnings(ctx context.Context, s *store.Store, serverID string, mods []*store.InstalledMod, results []modCompat) {
	// Dependencies that will be disabled (no build for the target): project id -> name.
	disabled := map[string]string{}
	for i := range results {
		if results[i].Status == compatIncompatible && mods[i].SourceID != nil {
			disabled[*mods[i].SourceID] = mods[i].Name
		}
	}
	if len(disabled) == 0 {
		return
	}

	edges, err := s.ListModDependencies(ctx, serverID)
	if err != nil {
		return
	}
	depsOf := map[string][]string{} // dependent project id -> its dependency project ids
	for _, e := range edges {
		depsOf[e.DependentProjectID] = append(depsOf[e.DependentProjectID], e.DependencyProjectID)
	}

	for i := range results {
		// Only mods that stay loaded can be broken by a missing dependency.
		if results[i].Status != compatCompatible && results[i].Status != compatSupported {
			continue
		}
		if mods[i].SourceID == nil {
			continue
		}
		seen := map[string]bool{}
		for _, dep := range depsOf[*mods[i].SourceID] {
			if name, ok := disabled[dep]; ok && !seen[dep] {
				seen[dep] = true
				results[i].DepWarnings = append(results[i].DepWarnings, name)
			}
		}
	}
}

// classifyForTarget asks the source which builds of a mod exist for the target MC
// version and decides the mod's bucket, filling Target* on a compatible move.
func classifyForTarget(ctx context.Context, src sourceClient, projectID, currentVersionID, loader, target string, out *modCompat) string {
	versions, err := src.GetVersions(ctx, projectID, loader, target)
	if err != nil {
		return compatUnknown
	}
	if len(versions) == 0 {
		return compatIncompatible
	}
	// If the build that's already installed is among the target-compatible ones,
	// nothing needs to change for this mod.
	for i := range versions {
		if versions[i].ID == currentVersionID {
			return compatSupported
		}
	}
	// Otherwise pick the newest build (API order is newest-first) that actually
	// has a downloadable file to move to.
	for i := range versions {
		if f := primaryFile(&versions[i]); f != nil && f.URL != "" {
			out.TargetVersion = versions[i].VersionNumber
			out.TargetVersionID = versions[i].ID
			return compatCompatible
		}
	}
	// Builds exist for the target but none are downloadable (author-disabled): we
	// can't move it, so it would be disabled like an incompatible mod.
	return compatIncompatible
}

// AutoUpdate kicks off an asynchronous safe-update run: apply available
// updates, restart, watch boot health, revert + blocklist anything that breaks
// the boot. Returns 202 with the run row; poll GET /mods/update-runs/{runId}.
func (h *ModHandlers) AutoUpdate(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	run, err := h.updater.Trigger(r.Context(), serverID, "manual")
	if err != nil {
		if errors.Is(err, autoupdate.ErrAlreadyRunning) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *ModHandlers) ListUpdateRuns(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := h.store.ListModUpdateRuns(r.Context(), serverID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*store.ModUpdateRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *ModHandlers) GetUpdateRun(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	run, err := h.store.GetModUpdateRun(r.Context(), chi.URLParam(r, "runId"))
	if err != nil || run.ServerID != serverID {
		writeError(w, http.StatusNotFound, "update run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *ModHandlers) ListSkippedVersions(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	rows, err := h.store.ListSkippedModVersions(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []*store.SkippedModVersion{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// UnskipVersion removes a version from the blocklist so the updater may try it
// again (e.g. after the mod author fixed the broken build in place).
func (h *ModHandlers) UnskipVersion(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	projectID := r.URL.Query().Get("project_id")
	versionID := r.URL.Query().Get("version_id")
	if projectID == "" || versionID == "" {
		writeError(w, http.StatusBadRequest, "project_id and version_id required")
		return
	}
	if err := h.store.DeleteSkippedModVersion(r.Context(), serverID, projectID, versionID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Update swaps an installed mod to a newer (or specified) version: deletes the
// old jar on the agent, uploads the new one, and updates the DB row in place.
func (h *ModHandlers) Update(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")

	var body struct {
		VersionID string `json:"version_id"`
	}
	_ = decode(r, &body)

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil || mod.ServerID != serverID {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}
	if mod.SourceID == nil {
		writeError(w, http.StatusBadRequest, "mod has no source project")
		return
	}
	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	ver, err := h.resolveVersion(ctx, srv, mod.Source, *mod.SourceID, body.VersionID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	file := primaryFile(ver)
	if file == nil {
		writeError(w, http.StatusBadRequest, "no files in version")
		return
	}
	if file.URL == "" {
		writeError(w, http.StatusBadGateway, "source does not permit downloading this file")
		return
	}

	tmpPath, err := h.modrinth.Download(ctx, file.URL, file.Hashes.SHA256)
	if err != nil {
		writeError(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)
	if err := verifyJarFile(tmpPath, file.Filename); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if err := uploadFileToAgent(ctx, c, serverID, mod.InstallPath, file.Filename, tmpPath); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Remove old jar if the filename changed (otherwise we just overwrote it).
	if file.Filename != mod.FileName {
		_ = deleteAgentFile(ctx, c, serverID, mod.InstallPath+"/"+mod.FileName)
	}

	sha := file.Hashes.SHA256
	vid := ver.ID
	mod.VersionID = &vid
	mod.Name = ver.Name
	mod.Version = ver.VersionNumber
	mod.FileName = file.Filename
	mod.SHA256 = &sha
	updated, err := h.store.UpdateMod(r.Context(), mod)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, serverID, "mod.update", map[string]any{"mod_id": modID, "version": ver.VersionNumber})
	writeJSON(w, http.StatusOK, updated)
}

// Pin toggles whether a mod is excluded from update checks/bulk updates.
func (h *ModHandlers) Pin(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")
	var body struct {
		Pinned bool `json:"pinned"`
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
	if err := h.store.SetModPinned(r.Context(), modID, body.Pinned); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// disabledSuffix marks a jar as not-to-be-loaded; Minecraft mod loaders skip
// files ending in it, so disabling is a rename rather than a delete.
