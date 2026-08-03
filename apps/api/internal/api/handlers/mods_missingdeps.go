package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

// MissingDepResolution is one loader mod id the server said was missing, mapped
// to something the panel can actually install. Found is false when no project
// matched; Error explains a project that matched but has no build for this
// server, so the dialog can say why instead of offering a doomed install.
type MissingDepResolution struct {
	ModID         string `json:"mod_id"`
	Found         bool   `json:"found"`
	ProjectID     string `json:"project_id,omitempty"`
	Slug          string `json:"slug,omitempty"`
	Title         string `json:"title,omitempty"`
	IconURL       string `json:"icon_url,omitempty"`
	VersionID     string `json:"version_id,omitempty"`
	VersionNumber string `json:"version_number,omitempty"`
	Error         string `json:"error,omitempty"`
}

// loaderModAliases maps a loader mod id to the Modrinth slug that provides it,
// for the cases where the two differ. Fabric API is the one that matters in
// practice: its jar declares the mod id "fabric" (with "fabric-api" as a newer
// alias), while Modrinth publishes it under the slug "fabric-api", so a direct
// slug lookup of "fabric" finds nothing or, worse, an unrelated project.
var loaderModAliases = map[string][]string{
	"fabric":     {"fabric-api"},
	"fabric-api": {"fabric-api"},
	// Fabric API's subsystems are declared as separate mod ids but all ship in
	// the one jar, so any of them going missing means Fabric API is missing.
	"fabric-api-base":              {"fabric-api"},
	"fabric-resource-loader-v0":    {"fabric-api"},
	"fabric-networking-api-v1":     {"fabric-api"},
	"fabric-lifecycle-events-v1":   {"fabric-api"},
	"fabric-command-api-v2":        {"fabric-api"},
	"fabric-registry-sync-v0":      {"fabric-api"},
	"fabric-entity-events-v1":      {"fabric-api"},
	"fabric-item-api-v1":           {"fabric-api"},
	"fabric-events-interaction-v0": {"fabric-api"},
	"fabric-transfer-api-v1":       {"fabric-api"},
	"fabric-screen-handler-api-v1": {"fabric-api"},

	// Other near-universal libraries whose loader id differs from their slug.
	"cloth-config2":      {"cloth-config"},
	"architectury":       {"architectury-api"},
	"forgeconfigapiport": {"forge-config-api-port"},
}

// uninstallableModIDs mirrors the agent's list: these name the platform, not a
// downloadable mod. The agent already filters them out of its suggestions; the
// API refuses them too so a hand-made request can't start a pointless install.
var uninstallableModIDs = map[string]bool{
	"minecraft":     true,
	"java":          true,
	"fabricloader":  true,
	"fabric-loader": true,
	"forge":         true,
	"neoforge":      true,
	"quilt_loader":  true,
}

// ResolveMissingDeps maps the loader mod ids from a missing-dependency startup
// failure to installable Modrinth projects, pinned to a build that fits this
// server's platform and Minecraft version. It only reads — the panel shows what
// it found and the operator confirms — so it is safe under mods:read.
func (h *ModHandlers) ResolveMissingDeps(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	var body struct {
		ModIDs []string `json:"mod_ids"`
	}
	if err := decode(r, &body); err != nil || len(body.ModIDs) == 0 {
		writeError(w, http.StatusBadRequest, "mod_ids required")
		return
	}
	if len(body.ModIDs) > 25 {
		body.ModIDs = body.ModIDs[:25]
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	installed, _ := h.store.ListMods(ctx, serverID)

	out := make([]MissingDepResolution, 0, len(body.ModIDs))
	seen := map[string]bool{}
	for _, id := range body.ModIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		out = append(out, h.resolveMissingDep(ctx, srv, installed, id))
	}

	writeJSON(w, http.StatusOK, out)
}

// resolveMissingDep resolves one loader mod id. It tries the alias table first
// (authoritative for the ids that don't match their slug), then a direct slug
// lookup, then a search — reporting what it found rather than guessing
// silently, because installing the wrong mod is worse than installing none.
func (h *ModHandlers) resolveMissingDep(ctx context.Context, srv *store.Server, installed []*store.InstalledMod, modID string) MissingDepResolution {
	res := MissingDepResolution{ModID: modID}

	if uninstallableModIDs[strings.ToLower(modID)] {
		res.Error = fmt.Sprintf("%q is part of the server platform, not an installable mod", modID)
		return res
	}

	proj := h.lookupMissingDepProject(ctx, srv, modID)
	if proj == nil {
		res.Error = fmt.Sprintf("couldn't find a mod providing %q on Modrinth", modID)
		return res
	}

	res.Found = true
	res.ProjectID = proj.ID
	res.Slug = proj.Slug
	res.Title = proj.Title
	res.IconURL = proj.IconURL

	// Already installed means the jar is present but the loader still couldn't
	// satisfy the dependency — usually the wrong build for this Minecraft
	// version. Installing again would not fix it, so say so plainly.
	for _, m := range installed {
		if m.SourceID != nil && *m.SourceID == proj.ID {
			res.Error = fmt.Sprintf("%s is already installed (%s) but doesn't satisfy the requirement — it may be the wrong version for %s, or disabled", proj.Title, m.Version, srv.MCVersion)
			return res
		}
	}

	loader := modrinth.LoaderForPlatform(srv.Platform)
	versions, err := h.modrinth.GetVersions(ctx, proj.ID, loader, srv.MCVersion)
	if err != nil {
		res.Error = "version lookup failed: " + err.Error()
		return res
	}
	if len(versions) == 0 {
		res.Error = fmt.Sprintf("%s has no build for %s %s", proj.Title, srv.Platform, srv.MCVersion)
		return res
	}
	res.VersionID = versions[0].ID
	res.VersionNumber = versions[0].VersionNumber
	return res
}

// lookupMissingDepProject finds the Modrinth project providing a loader mod id.
func (h *ModHandlers) lookupMissingDepProject(ctx context.Context, srv *store.Server, modID string) *modrinth.Project {
	// Copy rather than append onto the map's slice: appending to a map value
	// can write into its backing array and corrupt the alias table.
	aliases := loaderModAliases[strings.ToLower(modID)]
	candidates := make([]string, 0, len(aliases)+1)
	candidates = append(candidates, aliases...)
	candidates = append(candidates, modID)

	tried := map[string]bool{}
	for _, slug := range candidates {
		if slug == "" || tried[slug] {
			continue
		}
		tried[slug] = true
		if p, err := h.modrinth.GetProject(ctx, slug); err == nil && p != nil && p.ID != "" {
			return p
		}
	}

	// No slug matched. Search, and accept a hit only when its slug or title
	// matches the mod id — a fuzzy top hit for an unknown id is a coin flip, and
	// a wrong jar in mods/ is a worse problem than the one being fixed.
	sr, err := h.modrinth.Search(ctx, modrinth.SearchParams{
		Query:     modID,
		Loader:    modrinth.LoaderForPlatform(srv.Platform),
		MCVersion: srv.MCVersion,
		Limit:     10,
	})
	if err != nil || sr == nil {
		return nil
	}
	want := normalizeModID(modID)
	for _, hit := range sr.Hits {
		if normalizeModID(hit.Slug) == want || normalizeModID(hit.Title) == want {
			return &modrinth.Project{
				ID:      hit.ProjectID,
				Slug:    hit.Slug,
				Title:   hit.Title,
				IconURL: hit.IconURL,
			}
		}
	}
	return nil
}

// normalizeModID reduces an id or title to comparable letters and digits, so
// "cloth-config", "Cloth Config" and "clothconfig" all match.
func normalizeModID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
