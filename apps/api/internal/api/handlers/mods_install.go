package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

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

	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
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
