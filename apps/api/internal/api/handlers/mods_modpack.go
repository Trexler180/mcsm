package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
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

const (
	// maxMrpackEntries bounds how many files a single modpack may install and
	// maxMrpackFileBytes bounds each one, so a hostile pack can't exhaust the
	// agent's disk through a huge file count or one enormous / zip-bomb entry.
	maxMrpackEntries   = 10000
	maxMrpackFileBytes = 1 << 30 // 1 GiB per file
)

// applyMrpack reads the archive at packPath, installs server files + overrides
// onto the agent, and returns the number of files written. Every declared path
// is confined to the server root and every download is size-capped and verified
// against the manifest's SHA512 — the manifest is attacker-influenceable, so
// none of it is trusted.
func (h *ModHandlers) applyMrpack(ctx context.Context, c *agent.Client, serverID, packPath string) (int, error) {
	zr, err := zip.OpenReader(packPath)
	if err != nil {
		return 0, fmt.Errorf("open mrpack: %w", err)
	}
	defer zr.Close()

	// Parse the index manifest (size-bounded so a bloated manifest can't OOM us).
	var index *modrinth.MrpackIndex
	for _, f := range zr.File {
		if f.Name == "modrinth.index.json" {
			rc, err := f.Open()
			if err != nil {
				return 0, fmt.Errorf("open index: %w", err)
			}
			var idx modrinth.MrpackIndex
			err = json.NewDecoder(io.LimitReader(rc, 32<<20)).Decode(&idx)
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
	if len(index.Files) > maxMrpackEntries {
		return 0, fmt.Errorf("modpack declares too many files (%d)", len(index.Files))
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
		if count >= maxMrpackEntries {
			return count, fmt.Errorf("modpack exceeds %d-file limit", maxMrpackEntries)
		}
		rel, ok := cleanRelPath(mf.Path)
		if !ok {
			return count, fmt.Errorf("unsafe path in modpack manifest: %q", mf.Path)
		}
		dir, name := splitAgentPath(rel)
		tmp, err := h.modrinth.DownloadVerified(ctx, mf.Downloads[0], "", mf.Hashes.SHA512, maxMrpackFileBytes)
		if err != nil {
			return count, fmt.Errorf("download %s: %w", rel, err)
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
			cleaned, ok := cleanRelPath(rel)
			if !ok {
				return count, fmt.Errorf("unsafe override path in modpack: %q", f.Name)
			}
			if count >= maxMrpackEntries {
				return count, fmt.Errorf("modpack exceeds %d-file limit", maxMrpackEntries)
			}
			tmp, err := extractZipEntry(f, maxMrpackFileBytes)
			if err != nil {
				return count, err
			}
			dir, name := splitAgentPath(cleaned)
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

// cleanRelPath normalizes a modpack-declared path to a server-relative path and
// rejects anything that escapes the server root (".." traversal or an absolute
// path). The agent re-validates on write, but the panel must not single-tier a
// traversal defense.
func cleanRelPath(p string) (string, bool) {
	// Normalize backslashes too (not just the host separator), so a "..\.." entry
	// is caught regardless of the OS the API runs on.
	p = strings.TrimSpace(strings.ReplaceAll(filepath.ToSlash(p), "\\", "/"))
	p = strings.TrimPrefix(p, "/") // manifest paths are root-relative
	if p == "" {
		return "", false
	}
	// Clean as a *relative* path so a leading ".." survives (rooted-path cleaning
	// would silently drop it) and any escape is rejected rather than misplaced.
	clean := pathpkg.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// splitAgentPath turns "mods/foo.jar" into ("/mods", "foo.jar"); a bare filename
// yields ("/", name). Callers pass a path already cleaned by cleanRelPath.
func splitAgentPath(p string) (dir, name string) {
	p = strings.TrimPrefix(filepath.ToSlash(p), "/")
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "/", p
	}
	return "/" + p[:idx], p[idx+1:]
}

// extractZipEntry copies a zip entry to a temp file, capping the decompressed
// size (maxBytes > 0) so a zip-bomb override can't fill the disk.
func extractZipEntry(f *zip.File, maxBytes int64) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "mcsm-ovr-*")
	if err != nil {
		return "", err
	}
	var src io.Reader = rc
	if maxBytes > 0 {
		src = io.LimitReader(rc, maxBytes+1)
	}
	n, err := io.Copy(tmp, src)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if maxBytes > 0 && n > maxBytes {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("override entry %s exceeds %d-byte limit", f.Name, maxBytes)
	}
	return tmp.Name(), nil
}

// Updates lists installed mods that have a newer compatible version available.
