package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

func (h *ModHandlers) Search(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query       string   `json:"query"`
		Source      string   `json:"source"`
		Loader      string   `json:"loader"`
		MCVersion   string   `json:"mc_version"`
		ProjectType string   `json:"project_type"`
		Categories  []string `json:"categories"`
		Index       string   `json:"index"`
		Environment string   `json:"environment"`
		Limit       int      `json:"limit"`
		Offset      int      `json:"offset"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	params := modrinth.SearchParams{
		Query:       body.Query,
		Loader:      body.Loader,
		MCVersion:   body.MCVersion,
		ProjectType: body.ProjectType,
		Categories:  body.Categories,
		Index:       body.Index,
		Environment: body.Environment,
		Limit:       body.Limit,
		Offset:      body.Offset,
	}

	result, err := h.sourceFor(body.Source).Search(ctx, params)
	if err != nil {
		writeError(w, http.StatusBadGateway, body.Source+" search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Categories returns the category tags for a source, optionally filtered to a
// single project_type. Modrinth tags come from /v2/tag/category; CurseForge
// categories come from the Core API (keyed or key-less proxy) and carry the
// numeric id Search filters by. With CF search disabled the CF list is empty
// (200) and the frontend simply shows no chips. Hangar's fixed enum and
// SpigotMC's category list likewise carry the id Search filters by; both are
// plugin-only and answer empty for other project types.
func (h *ModHandlers) Categories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	projectType := r.URL.Query().Get("project_type")

	switch r.URL.Query().Get("source") {
	case "curseforge":
		if !h.curseforge.Enabled() {
			writeJSON(w, http.StatusOK, []modrinth.Category{})
			return
		}
		cats, err := h.curseforge.GetCategories(ctx, projectType)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cats)
		return
	case "hangar":
		cats, err := h.hangar.GetCategories(ctx, projectType)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cats)
		return
	case "spigotmc":
		cats, err := h.spigotmc.GetCategories(ctx, projectType)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cats)
		return
	}

	cats, err := h.modrinth.GetCategories(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	out := []modrinth.Category{}
	for _, c := range cats {
		if projectType == "" || c.ProjectType == projectType {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Sources reports which mod sources are searchable. CurseForge search works
// keyed (CURSEFORGE_API_KEY) or via the key-less proxy; it's only reported
// unavailable when the proxy is explicitly disabled without a key. Version
// checks, updates, and downloads of installed CF mods always work (key-less
// website API fallback in the curseforge package).
// Hangar and Spiget are anonymous APIs and always available.
func (h *ModHandlers) Sources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"modrinth":   true,
		"curseforge": h.curseforge.Enabled(),
		"hangar":     true,
		"spigotmc":   true,
	})
}

func (h *ModHandlers) GetVersions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	loader := r.URL.Query().Get("loader")
	mcVersion := r.URL.Query().Get("mc_version")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	versions, err := h.sourceFor(r.URL.Query().Get("source")).GetVersions(ctx, projectID, loader, mcVersion)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (h *ModHandlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	versionID := r.URL.Query().Get("version_id")
	projectID := r.URL.Query().Get("project_id")
	source := r.URL.Query().Get("source")
	if versionID == "" {
		writeError(w, http.StatusBadRequest, "version_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Non-Modrinth version ids are scoped under their project.
	if source != "" && source != "modrinth" && projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}
	version, err := h.versionFor(ctx, source, projectID, versionID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (h *ModHandlers) GetProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	project, err := h.sourceFor(r.URL.Query().Get("source")).GetProject(ctx, projectID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}
