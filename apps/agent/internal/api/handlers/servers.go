package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/install"
	"github.com/mcsm/agent/internal/process"
)

type ServerHandlers struct {
	mgr        *process.Manager
	serverRoot string
}

func NewServerHandlers(mgr *process.Manager, serverRoot string) *ServerHandlers {
	return &ServerHandlers{mgr: mgr, serverRoot: serverRoot}
}

// ScanImports lists existing server directories under the agent's server root
// with best-effort detected settings, so the panel can adopt a server already on
// disk. Read-only — it never touches the directories it reports.
func (h *ServerHandlers) ScanImports(w http.ResponseWriter, r *http.Request) {
	type candidate struct {
		Directory string `json:"directory"`
		AbsPath   string `json:"abs_path"`
		install.Detection
	}
	out := []candidate{}
	entries, err := os.ReadDir(h.serverRoot)
	if err != nil {
		writeJSON(w, http.StatusOK, out) // no root yet → nothing to import
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "mcsm-backups" || strings.HasPrefix(name, ".") {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(h.serverRoot, name))
		if err != nil {
			continue
		}
		if !install.LooksLikeServer(abs) {
			continue
		}
		out = append(out, candidate{
			Directory: name,
			AbsPath:   abs,
			Detection: install.Detect(abs),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ServerHandlers) Start(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var cfg process.StartConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if cfg.Directory == "" {
		writeError(w, http.StatusBadRequest, "directory is required")
		return
	}
	dir, err := validateServerDirectory(h.serverRoot, cfg.Directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.Directory = dir

	// Auto-fetch a server JAR (or run an installer) if the directory is empty.
	// Detached context with a generous deadline so a slow Spigot BuildTools
	// run (~5–10 min) or Forge/NeoForge installer doesn't get truncated.
	// Skipped for imported servers (NoInstall): their runtime is already on disk
	// and must not be overwritten.
	if cfg.Platform != "" && !cfg.NoInstall {
		dlCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		err := install.EnsureRuntime(dlCtx, cfg.Directory, cfg.Platform, cfg.MCVersion, cfg.JavaBinary)
		cancel()
		if err != nil {
			log.Printf("install runtime (%s %s): %v", cfg.Platform, cfg.MCVersion, err)
			writeError(w, http.StatusBadGateway, "auto-install: "+err.Error())
			return
		}
	}

	if err := h.mgr.Start(id, cfg); err != nil {
		if err.Error() == "server already running" {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

// Reinstall wipes the runtime jar/installer artifacts and re-fetches them for
// the requested platform + version. Requires the server to be stopped; the
// panel handles stopping first. Used when changing Minecraft/loader versions.
func (h *ServerHandlers) Reinstall(w http.ResponseWriter, r *http.Request) {
	var cfg process.StartConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if cfg.Directory == "" || cfg.Platform == "" {
		writeError(w, http.StatusBadRequest, "directory and platform required")
		return
	}
	dir, err := validateServerDirectory(h.serverRoot, cfg.Directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := install.Reinstall(ctx, dir, cfg.Platform, cfg.MCVersion, cfg.JavaBinary); err != nil {
		log.Printf("reinstall (%s %s): %v", cfg.Platform, cfg.MCVersion, err)
		writeError(w, http.StatusBadGateway, "reinstall: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "platform": cfg.Platform, "mc_version": cfg.MCVersion})
}

func (h *ServerHandlers) Stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Graceful   bool `json:"graceful"`
		TimeoutSec int  `json:"timeout_sec"`
	}
	body.Graceful = true
	body.TimeoutSec = 30
	_ = json.NewDecoder(r.Body).Decode(&body)

	timeout := time.Duration(body.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	if err := h.mgr.Stop(id, body.Graceful, timeout); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (h *ServerHandlers) Restart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.mgr.Restart(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func (h *ServerHandlers) Kill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.mgr.Kill(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

// DisableMods disables the given Fabric mod ids by renaming their jars to
// "<name>.disabled". Used to apply a detected mod-conflict fix.
func (h *ServerHandlers) DisableMods(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		ModIDs []string `json:"mod_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.ModIDs) == 0 {
		writeError(w, http.StatusBadRequest, "mod_ids is required")
		return
	}

	disabled, err := h.mgr.DisableConflictMods(id, body.ModIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if disabled == nil {
		disabled = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"disabled": disabled})
}

// Purge permanently deletes a server's on-disk data. The panel calls this when
// the operator opts into file/backup deletion while removing a server. Each
// target is independent: files wipes the live server directory, backups wipes
// the sibling mcsm-backups/<id> folder. The process is killed first so Windows
// releases its file locks on the world/jar.
func (h *ServerHandlers) Purge(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Directory string `json:"directory"`
		Files     bool   `json:"files"`
		Backups   bool   `json:"backups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Directory == "" {
		writeError(w, http.StatusBadRequest, "directory is required")
		return
	}
	// Confine deletion to within the configured server root — never RemoveAll an
	// arbitrary path supplied over the wire.
	dir, err := validateServerDirectory(h.serverRoot, body.Directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Best-effort kill; a server that isn't running just returns an error we
	// ignore. Without this, locked files on Windows would block RemoveAll.
	_ = h.mgr.Kill(id)

	if body.Backups {
		backupDir := filepath.Join(filepath.Dir(dir), "mcsm-backups", id)
		if err := os.RemoveAll(backupDir); err != nil {
			writeError(w, http.StatusInternalServerError, "delete backups: "+err.Error())
			return
		}
	}
	if body.Files {
		if err := os.RemoveAll(dir); err != nil {
			writeError(w, http.StatusInternalServerError, "delete files: "+err.Error())
			return
		}
	}

	h.mgr.Unregister(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *ServerHandlers) Status(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	info := h.mgr.Status(id)
	writeJSON(w, http.StatusOK, info)
}

func (h *ServerHandlers) Command(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	if err := h.mgr.SendCommand(id, body.Command); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
