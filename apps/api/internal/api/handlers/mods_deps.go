package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

// ── Removal impact ───────────────────────────────────────────────────
//
// Removing or disabling a library that other content requires is the classic way
// to break a working server: the jar disappears, and on the next boot every mod
// that depended on it fails to load — or the server refuses to start at all. The
// panel answers "what else breaks?" before the fact, from the dependency graph,
// and refuses the destructive call unless the operator confirmed with that answer
// in hand.

// DependentMod is one installed mod that declares a dependency on the mod being
// removed or disabled.
type DependentMod struct {
	ModID    string `json:"mod_id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Source   string `json:"source"`
	SourceID string `json:"source_id,omitempty"`
	Enabled  bool   `json:"enabled"`
	// "required" (won't load without it) or "optional" (uses it when present).
	DependencyType string `json:"dependency_type"`
}

// ModImpact is the answer to "what happens if this mod goes away?", for one mod
// on one server.
type ModImpact struct {
	ModID   string `json:"mod_id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Installed mods that require this one — the ones that break.
	Required []DependentMod `json:"required"`
	// Installed mods that list it as an optional dependency: they keep loading,
	// but lose whatever integration they built on it.
	Optional []DependentMod `json:"optional"`
	// Names of auto-installed dependencies of this mod that nothing else would
	// need once it's gone (they become removable, not broken).
	Orphaning []string `json:"orphaning"`
	// How many installed mods couldn't be checked at all, because their source
	// doesn't publish dependency data (CurseForge, SpigotMC) or they were
	// uploaded by hand. The dialog says so rather than implying "all clear".
	Unchecked int `json:"unchecked"`
	// False when the mod itself has no project identity (a hand-uploaded jar),
	// so nothing can reference it in the graph and the lists are empty by
	// construction rather than by evidence.
	Checked bool `json:"checked"`
}

// Breaking reports whether removing/disabling the mod would break other content.
func (i *ModImpact) Breaking() bool { return len(i.Required) > 0 }

// Dependents reports what else on the server depends on one installed mod. It
// refreshes the dependency graph first (bounded: one batched upstream call, and
// only when the installed set changed since the last scan), so the answer
// reflects the builds currently on disk rather than only what the panel happened
// to record at install time.
func (h *ModHandlers) Dependents(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil || mod.ServerID != serverID {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}

	mods, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeServerError(w, r, "mod dependents: list mods", err)
		return
	}
	h.ensureDependencyGraph(r.Context(), serverID, mods)

	impact, err := h.modImpact(r.Context(), serverID, mod, mods)
	if err != nil {
		writeServerError(w, r, "mod dependents", err)
		return
	}
	writeJSON(w, http.StatusOK, impact)
}

// modImpact assembles the impact report for one mod from the stored graph.
func (h *ModHandlers) modImpact(ctx context.Context, serverID string, mod *store.InstalledMod, mods []*store.InstalledMod) (*ModImpact, error) {
	impact := &ModImpact{
		ModID:     mod.ID,
		Name:      mod.Name,
		Enabled:   mod.Enabled,
		Required:  []DependentMod{},
		Optional:  []DependentMod{},
		Orphaning: []string{},
	}

	edges, err := h.store.ListModDependencies(ctx, serverID)
	if err != nil {
		return nil, err
	}

	// project id -> the installed mods carrying it (a project can legitimately
	// appear once per install directory).
	byProject := map[string][]*store.InstalledMod{}
	for _, m := range mods {
		if m.SourceID != nil && *m.SourceID != "" {
			byProject[*m.SourceID] = append(byProject[*m.SourceID], m)
		}
	}

	// Mods whose dependencies nothing can tell us about. Counting them keeps the
	// dialog honest: "nothing depends on this" would otherwise overstate what the
	// graph actually knows.
	hasOutgoing := map[string]bool{}
	for _, e := range edges {
		hasOutgoing[e.DependentProjectID] = true
	}
	for _, m := range mods {
		if m.ID == mod.ID {
			continue
		}
		if m.SourceID != nil && (dependencyAwareSource(m.Source) || hasOutgoing[*m.SourceID]) {
			continue
		}
		impact.Unchecked++
	}

	if mod.SourceID == nil || *mod.SourceID == "" {
		return impact, nil
	}
	impact.Checked = true
	target := *mod.SourceID

	seen := map[string]bool{}
	for _, e := range edges {
		if e.DependencyProjectID != target || e.DependentProjectID == target {
			continue
		}
		for _, dep := range byProject[e.DependentProjectID] {
			if dep.ID == mod.ID || seen[dep.ID] {
				continue
			}
			seen[dep.ID] = true
			entry := DependentMod{
				ModID:          dep.ID,
				Name:           dep.Name,
				Version:        dep.Version,
				Source:         dep.Source,
				Enabled:        dep.Enabled,
				DependencyType: e.Type,
			}
			if dep.SourceID != nil {
				entry.SourceID = *dep.SourceID
			}
			if e.Type == store.DependencyOptional {
				impact.Optional = append(impact.Optional, entry)
			} else {
				entry.DependencyType = store.DependencyRequired
				impact.Required = append(impact.Required, entry)
			}
		}
	}
	sortDependents(impact.Required)
	sortDependents(impact.Optional)

	impact.Orphaning = orphanedByRemoving(target, edges, byProject)
	return impact, nil
}

// orphanedByRemoving names the auto-installed dependencies of a mod that no
// other installed mod would still require once it is gone. They don't break —
// they just stop being needed, which is worth saying while the operator is
// already looking at a cleanup decision.
func orphanedByRemoving(target string, edges []store.ModDependency, byProject map[string][]*store.InstalledMod) []string {
	// Dependencies the target pulls in, and every other mod that also needs them.
	needs := map[string]bool{}
	othersNeed := map[string]bool{}
	for _, e := range edges {
		if e.Type == store.DependencyOptional {
			continue
		}
		if e.DependentProjectID == target {
			needs[e.DependencyProjectID] = true
			continue
		}
		// Only dependents that are actually still installed keep a dep alive.
		if len(byProject[e.DependentProjectID]) > 0 {
			othersNeed[e.DependencyProjectID] = true
		}
	}

	names := []string{}
	for pid := range needs {
		if othersNeed[pid] {
			continue
		}
		for _, m := range byProject[pid] {
			if m.InstalledAsDep {
				names = append(names, m.Name)
			}
		}
	}
	sort.Strings(names)
	// Never nil: this is serialized straight to the dialog, which indexes it.
	return names
}

func sortDependents(list []DependentMod) {
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

// dependencyAwareSource reports whether a source publishes machine-readable
// dependency data we can resolve. CurseForge's fingerprint API and SpigotMC's
// resource API don't, and a hand-uploaded jar has no project at all.
func dependencyAwareSource(source string) bool {
	return source == "modrinth" || source == "hangar"
}

// ── Graph refresh ────────────────────────────────────────────────────

// depScanTTL re-reads the graph occasionally even when nothing was installed, so
// an author editing a published version's dependency list eventually lands.
const depScanTTL = 24 * time.Hour

// depScanChunk bounds how many version ids go into one Modrinth request.
const depScanChunk = 50

// depScanRetry is how long a failed refresh is left alone. Without it, an
// upstream outage would have every Mods-tab load pay the full request timeout
// again, turning "Modrinth is down" into "the panel is slow".
const depScanRetry = 5 * time.Minute

// depScanBudget caps the whole refresh, so a slow upstream delays a list load by
// seconds rather than for as long as the client is willing to wait.
const depScanBudget = 10 * time.Second

// scanRecentlyFailed reports whether the last refresh for this server failed
// recently, and is where a failure gets recorded. In-memory on purpose: it's a
// backoff hint, and losing it on restart costs one extra attempt.
func (h *ModHandlers) scanRecentlyFailed(serverID string) bool {
	h.depScanMu.Lock()
	defer h.depScanMu.Unlock()
	at, ok := h.depScanFailed[serverID]
	return ok && time.Since(at) < depScanRetry
}

func (h *ModHandlers) noteScanFailure(serverID string, failed bool) {
	h.depScanMu.Lock()
	defer h.depScanMu.Unlock()
	if !failed {
		delete(h.depScanFailed, serverID)
		return
	}
	if h.depScanFailed == nil {
		h.depScanFailed = map[string]time.Time{}
	}
	h.depScanFailed[serverID] = time.Now()
}

// ensureDependencyGraph rebuilds the server's dependency edges from the metadata
// of the builds that are actually installed, replacing what the panel recorded at
// install time. This is what makes the graph true for jars the panel didn't
// install itself — uploaded by hand, copied in over SFTP, or adopted from disk and
// recognized by hash — which is precisely the content most likely to be removed
// without knowing what needed it.
//
// It is skipped entirely while the installed (project, version) set is unchanged
// and the last scan is recent, so the common Mods-tab load does no upstream work.
// Best-effort throughout: an unreachable source leaves the existing edges alone
// and defers the scan record, so the next load retries.
func (h *ModHandlers) ensureDependencyGraph(ctx context.Context, serverID string, mods []*store.InstalledMod) {
	fingerprint := modSetFingerprint(mods)
	if prev, at, err := h.store.ModDependencyScan(ctx, serverID); err == nil {
		if prev == fingerprint && !at.IsZero() && time.Since(at) < depScanTTL {
			return
		}
	}
	if h.scanRecentlyFailed(serverID) {
		return
	}

	// Only Modrinth is refreshed in bulk: it resolves a whole server in one
	// request. Hangar publishes dependencies too but only per version, so its
	// edges stay as recorded at install time rather than costing one HTTP call
	// per plugin on a Mods-tab load.
	var ids []string
	seen := map[string]bool{}
	for _, m := range mods {
		if m.Source != "modrinth" || m.SourceID == nil || m.VersionID == nil || *m.VersionID == "" {
			continue
		}
		if seen[*m.VersionID] {
			continue
		}
		seen[*m.VersionID] = true
		ids = append(ids, *m.VersionID)
	}
	if len(ids) == 0 {
		// Nothing resolvable — record the scan anyway so an all-custom server
		// doesn't retry on every load.
		_ = h.store.SetModDependencyScan(ctx, serverID, fingerprint)
		return
	}

	cctx, cancel := context.WithTimeout(ctx, depScanBudget)
	defer cancel()

	complete := true
	for start := 0; start < len(ids); start += depScanChunk {
		end := start + depScanChunk
		if end > len(ids) {
			end = len(ids)
		}
		versions, err := h.modrinth.GetVersionsByIDs(cctx, ids[start:end])
		if err != nil {
			complete = false
			break
		}
		for _, v := range versions {
			if v.ProjectID == "" {
				continue
			}
			if err := h.store.ReplaceModDependencies(ctx, serverID, v.ProjectID, declaredDeps(v)); err != nil {
				complete = false
			}
		}
	}
	h.noteScanFailure(serverID, !complete)
	if complete {
		_ = h.store.SetModDependencyScan(ctx, serverID, fingerprint)
	}
}

// declaredDeps converts a version's upstream dependency list to graph edges,
// keeping required and optional entries and dropping the kinds that say nothing
// about what would break ("incompatible", "embedded" — an embedded dependency
// ships inside the jar, so removing the separate copy breaks nothing).
func declaredDeps(v modrinth.Version) []store.ModDependency {
	out := make([]store.ModDependency, 0, len(v.Dependencies))
	for _, d := range v.Dependencies {
		if d.ProjectID == "" {
			continue
		}
		switch d.DependencyType {
		case "required":
			out = append(out, store.ModDependency{DependencyProjectID: d.ProjectID, Type: store.DependencyRequired})
		case "optional":
			out = append(out, store.ModDependency{DependencyProjectID: d.ProjectID, Type: store.DependencyOptional})
		}
	}
	return out
}

// modSetFingerprint digests the installed (source, project, version) triples. Any
// install, update, version switch or removal changes it; reordering or unrelated
// metadata edits don't.
func modSetFingerprint(mods []*store.InstalledMod) string {
	lines := make([]string, 0, len(mods))
	for _, m := range mods {
		if m.SourceID == nil || *m.SourceID == "" {
			continue
		}
		vid := ""
		if m.VersionID != nil {
			vid = *m.VersionID
		}
		lines = append(lines, m.Source+"|"+*m.SourceID+"|"+vid)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// ── Shared guards ────────────────────────────────────────────────────

// checkDependencyImpact loads the impact report for a mod and, unless force is
// set, writes a 409 carrying it when other content requires the mod. Callers stop
// on ok == false. This is the API-level half of the warning: the dialog can be
// bypassed, a scripted DELETE can't be — it has to say it accepts the breakage.
func (h *ModHandlers) checkDependencyImpact(w http.ResponseWriter, r *http.Request, serverID string, mod *store.InstalledMod, force bool, verb string) (*ModImpact, bool) {
	mods, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeServerError(w, r, "dependency impact: list mods", err)
		return nil, false
	}
	h.ensureDependencyGraph(r.Context(), serverID, mods)

	impact, err := h.modImpact(r.Context(), serverID, mod, mods)
	if err != nil {
		writeServerError(w, r, "dependency impact", err)
		return nil, false
	}
	if force || !impact.Breaking() {
		return impact, true
	}

	names := make([]string, 0, len(impact.Required))
	for _, d := range impact.Required {
		names = append(names, d.Name)
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": fmt.Sprintf("%s %s would break %s — pass force to proceed",
			verb, mod.Name, strings.Join(names, ", ")),
		"impact": impact,
	})
	return nil, false
}

// disableDependents turns off the mods in the list that are still enabled,
// renaming each jar on the agent the same way SetEnabled does. It reports the
// names it disabled and the ones it couldn't — the caller has to say so, because
// a dependent left loaded against a dependency that just disappeared is exactly
// the failure the operator was trying to avoid.
//
// Best-effort per mod: one failure doesn't abandon the rest, since the
// destructive part already happened and stopping halfway leaves a worse mix.
func (h *ModHandlers) disableDependents(ctx context.Context, c *agent.Client, serverID string, dependents []DependentMod) (done, failed []string) {
	for _, d := range dependents {
		if !d.Enabled {
			continue
		}
		mod, err := h.store.GetMod(ctx, d.ModID)
		if err != nil || mod.ServerID != serverID {
			failed = append(failed, d.Name)
			continue
		}
		if !mod.Enabled {
			continue // already off; nothing to do
		}
		newName := mod.FileName
		if !strings.HasSuffix(newName, disabledSuffix) {
			newName += disabledSuffix
		}
		if err := renameAgentFile(ctx, c, serverID, mod.InstallPath+"/"+mod.FileName, mod.InstallPath+"/"+newName); err != nil {
			failed = append(failed, mod.Name)
			continue
		}
		if err := h.store.SetModEnabled(ctx, mod.ID, false, newName); err != nil {
			// The jar is renamed but the row still says enabled; reconciliation
			// on the next Mods-tab load fixes the record.
			failed = append(failed, mod.Name)
			continue
		}
		done = append(done, mod.Name)
	}
	return done, failed
}
