package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
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
	if cfg.Platform != "" {
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
