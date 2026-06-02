package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/curseforge"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

type ModHandlers struct {
	store      *store.Store
	modrinth   *modrinth.Client
	curseforge *curseforge.Client
}

func NewModHandlers(s *store.Store) *ModHandlers {
	return &ModHandlers{store: s, modrinth: modrinth.New(), curseforge: curseforge.New()}
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
	writeJSON(w, http.StatusOK, mods)
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
		Limit:       body.Limit,
		Offset:      body.Offset,
	}

	var result *modrinth.SearchResult
	var err error
	if body.Source == "curseforge" {
		result, err = h.curseforge.Search(ctx, params)
	} else {
		result, err = h.modrinth.Search(ctx, params)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, body.Source+" search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Sources reports which mod sources are available (CurseForge needs an API key).
func (h *ModHandlers) Sources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"modrinth":   true,
		"curseforge": h.curseforge.Enabled(),
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

	var versions []modrinth.Version
	var err error
	if r.URL.Query().Get("source") == "curseforge" {
		versions, err = h.curseforge.GetVersions(ctx, projectID, loader, mcVersion)
	} else {
		versions, err = h.modrinth.GetVersions(ctx, projectID, loader, mcVersion)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (h *ModHandlers) GetProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var project *modrinth.Project
	var err error
	if r.URL.Query().Get("source") == "curseforge" {
		project, err = h.curseforge.GetProject(ctx, projectID)
	} else {
		project, err = h.modrinth.GetProject(ctx, projectID)
	}
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

	// Dependency resolution is currently a Modrinth-only capability.
	withDeps := body.WithDeps && body.Source == "modrinth"
	installed, err := h.installRecursive(ctx, c, srv, body.Source, body.ProjectID, body.VersionID, withDeps, false, map[string]bool{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(h.store, r, serverID, "mod.install", map[string]any{"source": body.Source, "project_id": body.ProjectID, "count": len(installed)})
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
	loader := modrinth.LoaderForPlatform(srv.Platform)
	if source == "curseforge" {
		if versionID != "" {
			return h.curseforge.GetVersion(ctx, projectID, versionID)
		}
		versions, err := h.curseforge.GetVersions(ctx, projectID, loader, srv.MCVersion)
		if err != nil {
			return nil, fmt.Errorf("version lookup failed: %w", err)
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("no compatible version for %s %s", srv.Platform, srv.MCVersion)
		}
		return &versions[0], nil
	}
	if versionID != "" {
		return h.modrinth.GetVersion(ctx, versionID)
	}
	versions, err := h.modrinth.GetVersions(ctx, projectID, loader, srv.MCVersion)
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
	out := []updateInfo{}
	for _, m := range mods {
		if m.Source != "modrinth" || m.SourceID == nil || m.Pinned {
			continue
		}
		versions, err := h.modrinth.GetVersions(ctx, *m.SourceID, loader, srv.MCVersion)
		if err != nil || len(versions) == 0 {
			continue
		}
		latest := versions[0]
		if m.VersionID != nil && latest.ID != *m.VersionID {
			out = append(out, updateInfo{
				ModID:           m.ID,
				Name:            m.Name,
				CurrentVersion:  m.Version,
				LatestVersion:   latest.VersionNumber,
				LatestVersionID: latest.ID,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
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
	audit(h.store, r, serverID, "mod.uninstall", map[string]any{"mod_id": modID, "name": mod.Name})
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ──────────────────────────────────────────────────────────

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
