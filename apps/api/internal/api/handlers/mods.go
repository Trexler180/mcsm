package handlers

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/mods/curseforge"
	"github.com/mcsm/api/internal/mods/hangar"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/mods/spigotmc"
	"github.com/mcsm/api/internal/store"
)

type ModHandlers struct {
	store      *store.Store
	modrinth   *modrinth.Client
	curseforge *curseforge.Client
	hangar     *hangar.Client
	spigotmc   *spigotmc.Client
	updater    *autoupdate.Engine
}

func NewModHandlers(s *store.Store, updater *autoupdate.Engine) *ModHandlers {
	return &ModHandlers{
		store:    s,
		modrinth: modrinth.New(),
		// Resolve the CurseForge key lazily from the encrypted secret store,
		// falling back to the legacy env var, so a key pasted in Settings →
		// Integrations takes effect without a restart.
		curseforge: curseforge.New(func() string {
			if v, _ := s.GetSecret(context.Background(), "curseforge_api_key"); v != "" {
				return v
			}
			return os.Getenv("CURSEFORGE_API_KEY")
		}),
		hangar:   hangar.New(),
		spigotmc: spigotmc.New(),
		updater:  updater,
	}
}

// sourceClient is the slice of the per-source clients the generic handlers
// dispatch over; every source normalizes into the modrinth wire shapes.
type sourceClient interface {
	Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error)
	GetProject(ctx context.Context, projectID string) (*modrinth.Project, error)
	GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
}

func (h *ModHandlers) sourceFor(source string) sourceClient {
	switch source {
	case "curseforge":
		return h.curseforge
	case "hangar":
		return h.hangar
	case "spigotmc":
		return h.spigotmc
	default:
		return h.modrinth
	}
}

// versionFor fetches one version; it exists because Modrinth version ids are
// globally unique while the other sources scope them under a project.
func (h *ModHandlers) versionFor(ctx context.Context, source, projectID, versionID string) (*modrinth.Version, error) {
	switch source {
	case "curseforge":
		return h.curseforge.GetVersion(ctx, projectID, versionID)
	case "hangar":
		return h.hangar.GetVersion(ctx, projectID, versionID)
	case "spigotmc":
		return h.spigotmc.GetVersion(ctx, projectID, versionID)
	default:
		return h.modrinth.GetVersion(ctx, versionID)
	}
}

func (h *ModHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mods, err := h.store.ListMods(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mods == nil {
		mods = []*store.InstalledMod{}
	}
	if err := h.annotateDependencies(r.Context(), id, mods); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mods)
}

// annotateDependencies fills RequiredBy/Orphaned on each mod from the reverse
// dependency graph. A mod is orphaned when it was auto-installed as a dependency
// but no currently-installed mod still requires it.
func (h *ModHandlers) annotateDependencies(ctx context.Context, serverID string, mods []*store.InstalledMod) error {
	edges, err := h.store.ListModDependencies(ctx, serverID)
	if err != nil {
		return err
	}

	// project id -> display name, for resolving dependent names. Only count
	// dependents that are actually still installed.
	nameByPID := map[string]string{}
	for _, m := range mods {
		if m.SourceID != nil {
			nameByPID[*m.SourceID] = m.Name
		}
	}
	// dependency project id -> set of dependent project ids that still exist.
	dependents := map[string]map[string]bool{}
	for _, e := range edges {
		if _, ok := nameByPID[e.DependentProjectID]; !ok {
			continue // dependent no longer installed
		}
		if dependents[e.DependencyProjectID] == nil {
			dependents[e.DependencyProjectID] = map[string]bool{}
		}
		dependents[e.DependencyProjectID][e.DependentProjectID] = true
	}

	for _, m := range mods {
		m.RequiredBy = []string{}
		if m.SourceID == nil {
			continue
		}
		for pid := range dependents[*m.SourceID] {
			m.RequiredBy = append(m.RequiredBy, nameByPID[pid])
		}
		m.Orphaned = m.InstalledAsDep && len(m.RequiredBy) == 0
	}
	return nil
}

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

func (h *ModHandlers) Install(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	var body struct {
		Source    string `json:"source"`
		ProjectID string `json:"project_id"`
		VersionID string `json:"version_id"`
		WithDeps  bool   `json:"with_deps"`
	}
	if err := decode(r, &body); err != nil || body.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}
	if body.Source == "" {
		body.Source = "modrinth"
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	// Dependency resolution needs the source to publish dependency project ids:
	// Modrinth and Hangar do; CurseForge and Spiget don't.
	withDeps := body.WithDeps && (body.Source == "modrinth" || body.Source == "hangar")
	installed, err := h.installRecursive(ctx, c, srv, body.Source, body.ProjectID, body.VersionID, withDeps, false, map[string]bool{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(h.store, r, serverID, "mod.install", map[string]any{"source": body.Source, "project_id": body.ProjectID, "count": len(installed)})
	writeJSON(w, http.StatusCreated, installed)
}

const customModUploadLimit = 512 << 20

// UploadCustom installs user-supplied jar files into the server's mod/plugin
// directory and records them in installed_mods so the panel can manage them.
func (h *ModHandlers) UploadCustom(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, customModUploadLimit)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse jar upload")
		return
	}
	if r.MultipartForm == nil {
		writeError(w, http.StatusBadRequest, "no jar files uploaded")
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no jar files uploaded")
		return
	}

	dest := customInstallDirForPlatform(srv.Platform)
	existing, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	installed := make([]*store.InstalledMod, 0, len(files))
	for _, fh := range files {
		name := cleanUploadFilename(fh.Filename)
		if name == "" || !strings.EqualFold(filepath.Ext(name), ".jar") {
			writeError(w, http.StatusBadRequest, "custom uploads must be .jar files")
			return
		}

		tracked := findTrackedUpload(existing, dest, name)
		if tracked != nil && strings.HasSuffix(tracked.FileName, disabledSuffix) {
			writeError(w, http.StatusConflict, fmt.Sprintf("%s is currently disabled; enable or uninstall it before replacing the jar", strings.TrimSuffix(tracked.FileName, disabledSuffix)))
			return
		}
		if tracked != nil && tracked.Source != "custom" {
			writeError(w, http.StatusConflict, fmt.Sprintf("%s is already tracked from %s; uninstall it before uploading a custom replacement", tracked.FileName, tracked.Source))
			return
		}

		tmpPath, sha, err := saveUploadedJar(fh)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := uploadFileToAgent(ctx, c, serverID, dest, name, tmpPath); err != nil {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		os.Remove(tmpPath)

		modName := strings.TrimSuffix(name, filepath.Ext(name))
		if tracked != nil {
			tracked.SourceID = nil
			tracked.VersionID = nil
			tracked.Name = modName
			tracked.Version = "custom"
			tracked.FileName = name
			tracked.SHA256 = &sha
			updated, err := h.store.UpdateMod(r.Context(), tracked)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			installed = append(installed, updated)
			continue
		}

		mod, err := h.store.CreateMod(r.Context(), &store.InstalledMod{
			ServerID:    serverID,
			Source:      "custom",
			Name:        modName,
			Version:     "custom",
			FileName:    name,
			SHA256:      &sha,
			InstallPath: dest,
		})
		if err != nil {
			_ = deleteAgentFile(ctx, c, serverID, dest+"/"+name)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		existing = append(existing, mod)
		installed = append(installed, mod)
	}

	audit(h.store, r, serverID, "mod.upload", map[string]any{"count": len(installed), "install_path": dest})
	writeJSON(w, http.StatusCreated, installed)
}

// installRecursive resolves, downloads (verified), uploads to the agent, and
// records one mod — then, when withDeps is set, recurses over its required
// dependencies. visited guards against dependency cycles and re-installs.
func (h *ModHandlers) installRecursive(ctx context.Context, c *agent.Client, srv *store.Server, source, projectID, versionID string, withDeps, asDep bool, visited map[string]bool) ([]*store.InstalledMod, error) {
	if visited[projectID] {
		return nil, nil
	}
	visited[projectID] = true

	// Skip if already installed.
	existing, _ := h.store.ListMods(ctx, srv.ID)
	for _, m := range existing {
		if m.SourceID != nil && *m.SourceID == projectID {
			return nil, nil
		}
	}

	ver, err := h.resolveVersion(ctx, srv, source, projectID, versionID)
	if err != nil {
		return nil, err
	}

	file := primaryFile(ver)
	if file == nil {
		return nil, fmt.Errorf("no files in version for %s", projectID)
	}
	if file.URL == "" {
		return nil, fmt.Errorf("%s does not permit third-party downloads of this file", source)
	}

	dest := installDirForVersion(ver, srv.Platform)
	mod, err := h.downloadAndRecord(ctx, c, srv.ID, source, projectID, ver, file, dest, asDep)
	if err != nil {
		return nil, err
	}
	result := []*store.InstalledMod{mod}

	if withDeps {
		for _, dep := range ver.Dependencies {
			if dep.DependencyType != "required" || dep.ProjectID == "" {
				continue
			}
			// Record the edge before (maybe) installing: even if the dep is
			// already present, this mod now counts as one of its dependents, so
			// it won't be flagged orphaned while we still need it.
			if err := h.store.AddModDependency(ctx, srv.ID, projectID, dep.ProjectID); err != nil {
				return result, fmt.Errorf("record dependency edge: %w", err)
			}
			sub, err := h.installRecursive(ctx, c, srv, source, dep.ProjectID, dep.VersionID, true, true, visited)
			if err != nil {
				// Best-effort on deps: surface but don't roll back the main mod.
				return result, fmt.Errorf("dependency %s failed: %w", dep.ProjectID, err)
			}
			result = append(result, sub...)
		}
	}
	return result, nil
}

// resolveVersion returns the explicit version when versionID is set, else the
// newest version compatible with the server's loader + MC version, for the
// chosen source.
func (h *ModHandlers) resolveVersion(ctx context.Context, srv *store.Server, source, projectID, versionID string) (*modrinth.Version, error) {
	if versionID != "" {
		return h.versionFor(ctx, source, projectID, versionID)
	}
	loader := modrinth.LoaderForPlatform(srv.Platform)
	versions, err := h.sourceFor(source).GetVersions(ctx, projectID, loader, srv.MCVersion)
	if err != nil {
		return nil, fmt.Errorf("version lookup failed: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no compatible version for %s %s", srv.Platform, srv.MCVersion)
	}
	return &versions[0], nil
}

// downloadAndRecord fetches the jar (verifying SHA256), uploads it to the agent
// install dir, and writes the installed_mods row.
func (h *ModHandlers) downloadAndRecord(ctx context.Context, c *agent.Client, serverID, source, projectID string, ver *modrinth.Version, file *modrinth.VersionFile, dest string, asDep bool) (*store.InstalledMod, error) {
	tmpPath, err := h.modrinth.Download(ctx, file.URL, file.Hashes.SHA256)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpPath)
	if err := verifyJarFile(tmpPath, file.Filename); err != nil {
		return nil, err
	}

	if err := uploadFileToAgent(ctx, c, serverID, dest, file.Filename, tmpPath); err != nil {
		return nil, err
	}

	sha := file.Hashes.SHA256
	pid := projectID
	vid := ver.ID
	return h.store.CreateMod(ctx, &store.InstalledMod{
		ServerID:       serverID,
		Source:         source,
		SourceID:       &pid,
		VersionID:      &vid,
		Name:           ver.Name,
		Version:        ver.VersionNumber,
		FileName:       file.Filename,
		SHA256:         &sha,
		InstallPath:    dest,
		InstalledAsDep: asDep,
	})
}

// InstallModpack downloads a Modrinth .mrpack, installs every server-side file
// to its declared path, applies the pack's overrides, and records the modpack as
// a single installed entry. CurseForge modpacks use a different manifest format
// and are not supported here.
func (h *ModHandlers) InstallModpack(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	var body struct {
		ProjectID string `json:"project_id"`
		VersionID string `json:"version_id"`
	}
	if err := decode(r, &body); err != nil || body.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()

	ver, err := h.resolveVersion(ctx, srv, "modrinth", body.ProjectID, body.VersionID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	file := primaryFile(ver)
	if file == nil || !strings.HasSuffix(file.Filename, ".mrpack") {
		writeError(w, http.StatusBadRequest, "selected version is not a .mrpack")
		return
	}

	packPath, err := h.modrinth.Download(ctx, file.URL, file.Hashes.SHA256)
	if err != nil {
		writeError(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer os.Remove(packPath)

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	count, err := h.applyMrpack(ctx, c, serverID, packPath)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Record the modpack itself as one entry for visibility.
	sha := file.Hashes.SHA256
	pid := body.ProjectID
	vid := ver.ID
	mod, err := h.store.CreateMod(ctx, &store.InstalledMod{
		ServerID:    serverID,
		Source:      "modrinth",
		SourceID:    &pid,
		VersionID:   &vid,
		Name:        ver.Name,
		Version:     ver.VersionNumber,
		FileName:    file.Filename,
		SHA256:      &sha,
		InstallPath: "/",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, serverID, "modpack.install", map[string]any{"project_id": body.ProjectID, "files": count})
	writeJSON(w, http.StatusCreated, map[string]any{"modpack": mod, "files_installed": count})
}

// applyMrpack reads the archive at packPath, installs server files + overrides
// onto the agent, and returns the number of files written.
func (h *ModHandlers) applyMrpack(ctx context.Context, c *agent.Client, serverID, packPath string) (int, error) {
	zr, err := zip.OpenReader(packPath)
	if err != nil {
		return 0, fmt.Errorf("open mrpack: %w", err)
	}
	defer zr.Close()

	// Parse the index manifest.
	var index *modrinth.MrpackIndex
	for _, f := range zr.File {
		if f.Name == "modrinth.index.json" {
			rc, err := f.Open()
			if err != nil {
				return 0, fmt.Errorf("open index: %w", err)
			}
			var idx modrinth.MrpackIndex
			err = json.NewDecoder(rc).Decode(&idx)
			rc.Close()
			if err != nil {
				return 0, fmt.Errorf("parse index: %w", err)
			}
			index = &idx
			break
		}
	}
	if index == nil {
		return 0, fmt.Errorf("modrinth.index.json missing from pack")
	}

	count := 0
	// 1. Downloaded files declared in the manifest (skip client-only).
	for _, mf := range index.Files {
		if mf.Env.Server == "unsupported" {
			continue
		}
		if len(mf.Downloads) == 0 {
			continue
		}
		dir, name := splitAgentPath(mf.Path)
		tmp, err := h.modrinth.Download(ctx, mf.Downloads[0], "")
		if err != nil {
			return count, fmt.Errorf("download %s: %w", mf.Path, err)
		}
		err = uploadFileToAgent(ctx, c, serverID, dir, name, tmp)
		os.Remove(tmp)
		if err != nil {
			return count, err
		}
		count++
	}

	// 2. Overrides bundled in the archive. server-overrides win over overrides.
	for _, prefix := range []string{"overrides/", "server-overrides/"} {
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || !strings.HasPrefix(f.Name, prefix) {
				continue
			}
			rel := strings.TrimPrefix(f.Name, prefix)
			if rel == "" {
				continue
			}
			tmp, err := extractZipEntry(f)
			if err != nil {
				return count, err
			}
			dir, name := splitAgentPath(rel)
			err = uploadFileToAgent(ctx, c, serverID, dir, name, tmp)
			os.Remove(tmp)
			if err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// splitAgentPath turns "mods/foo.jar" into ("/mods", "foo.jar"); a bare filename
// yields ("/", name).
func splitAgentPath(p string) (dir, name string) {
	p = strings.TrimPrefix(filepath.ToSlash(p), "/")
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "/", p
	}
	return "/" + p[:idx], p[idx+1:]
}

func extractZipEntry(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "mcsm-ovr-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, rc)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// Updates lists installed mods that have a newer compatible version available.
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
				CurrentVersion:  m.Version,
				LatestVersion:   target.VersionNumber,
				LatestVersionID: target.ID,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
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
	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
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

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	newName := mod.FileName
	if body.Enabled {
		newName = strings.TrimSuffix(mod.FileName, disabledSuffix)
	} else if !strings.HasSuffix(mod.FileName, disabledSuffix) {
		newName = mod.FileName + disabledSuffix
	}

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
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

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
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

	audit(h.store, r, serverID, "mod.disable_conflict", map[string]any{"mod_ids": body.ModIDs, "disabled": disabled})
	writeJSON(w, http.StatusOK, map[string]any{"disabled": disabled})
}

func (h *ModHandlers) Uninstall(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil || mod.ServerID != serverID {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
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
func verifyJarFile(path, filename string) error {
	if !strings.EqualFold(filepath.Ext(filename), ".jar") {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || magic[0] != 'P' || magic[1] != 'K' {
		return fmt.Errorf("downloaded file is not a valid jar (the source may require downloading it manually)")
	}
	return nil
}

func primaryFile(ver *modrinth.Version) *modrinth.VersionFile {
	for i := range ver.Files {
		if ver.Files[i].Primary {
			return &ver.Files[i]
		}
	}
	if len(ver.Files) > 0 {
		return &ver.Files[0]
	}
	return nil
}

// installDirForVersion picks the on-disk target dir from the version's loaders
// and the server platform: datapacks → world/datapacks, Bukkit-style plugins →
// plugins, everything else → mods.
func installDirForVersion(ver *modrinth.Version, platform string) string {
	loaders := map[string]bool{}
	for _, l := range ver.Loaders {
		loaders[strings.ToLower(l)] = true
	}
	if loaders["datapack"] {
		return "/world/datapacks"
	}
	if loaders["paper"] || loaders["spigot"] || loaders["bukkit"] || loaders["purpur"] || loaders["folia"] {
		return "/plugins"
	}
	if modrinth.IsPluginPlatform(platform) {
		return "/plugins"
	}
	return "/mods"
}

func customInstallDirForPlatform(platform string) string {
	if modrinth.IsPluginPlatform(platform) {
		return "/plugins"
	}
	return "/mods"
}

func cleanUploadFilename(filename string) string {
	name := pathpkg.Base(strings.ReplaceAll(filename, "\\", "/"))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func findTrackedUpload(mods []*store.InstalledMod, installPath, filename string) *store.InstalledMod {
	disabledName := filename + disabledSuffix
	for _, m := range mods {
		if m.InstallPath != installPath {
			continue
		}
		if strings.EqualFold(m.FileName, filename) || strings.EqualFold(m.FileName, disabledName) {
			return m
		}
	}
	return nil
}

func saveUploadedJar(fh *multipart.FileHeader) (path string, sha string, err error) {
	src, err := fh.Open()
	if err != nil {
		return "", "", fmt.Errorf("open upload: %w", err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp("", "mcsm-custom-mod-*.jar")
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), src); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return tmp.Name(), hex.EncodeToString(hash.Sum(nil)), nil
}

// uploadFileToAgent streams a local file to the agent's upload endpoint without
// buffering the whole jar in memory (A5): an io.Pipe feeds a multipart writer
// that copies straight from disk.
func uploadFileToAgent(ctx context.Context, c *agent.Client, serverID, destDir, filename, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	defer f.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, err := mw.CreateFormFile("files", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(fw, f); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(mw.Close())
	}()

	uploadURL := fmt.Sprintf("%s/agent/v1/servers/%s/files/upload?path=%s",
		c.BaseURL, serverID, url.QueryEscape(destDir))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("upload to agent failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("agent upload returned %d", resp.StatusCode)
	}
	return nil
}

// renameAgentFile moves a file within the server directory on the agent (used to
// toggle the .disabled suffix). from/to are server-relative paths like
// "/mods/foo.jar".
func renameAgentFile(ctx context.Context, c *agent.Client, serverID, from, to string) error {
	renameURL := fmt.Sprintf("%s/agent/v1/servers/%s/files/rename", c.BaseURL, serverID)
	payload, err := json.Marshal(map[string]string{"from": from, "to": to})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, renameURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	return nil
}

func deleteAgentFile(ctx context.Context, c *agent.Client, serverID, path string) error {
	delURL := fmt.Sprintf("%s/agent/v1/servers/%s/files?path=%s",
		c.BaseURL, serverID, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	return nil
}
