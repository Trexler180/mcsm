package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/mc"
	"github.com/mcsm/api/internal/migrate"
	"github.com/mcsm/api/internal/store"
)

// MigrationHandlers expose the server version-migration engine over HTTP: kick
// off a migration, list past runs, and poll one for live progress.
type MigrationHandlers struct {
	store  *store.Store
	engine *migrate.Engine
	mc     *mc.Client
}

func NewMigrationHandlers(s *store.Store, engine *migrate.Engine) *MigrationHandlers {
	return &MigrationHandlers{store: s, engine: engine, mc: mc.New()}
}

// Migrate starts an asynchronous version migration: back up, change the server
// version, move compatible mods, disable incompatible ones, restart and watch
// the boot, restoring the backup if it comes up unhealthy. Returns 202 with the
// run row; poll GET /servers/{id}/migrations/{runId}. Body: {mc_version}.
func (h *MigrationHandlers) Migrate(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	var body struct {
		MCVersion string `json:"mc_version"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.MCVersion) == "" {
		writeError(w, http.StatusBadRequest, "mc_version required")
		return
	}
	target := strings.TrimSpace(body.MCVersion)

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if target == srv.MCVersion {
		writeError(w, http.StatusBadRequest, "server is already on "+target)
		return
	}

	// Soft-validate the target: reject an obvious typo, but proceed if the
	// upstream version list is momentarily unavailable rather than block.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
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

	run, err := h.engine.Trigger(r.Context(), serverID, target)
	if err != nil {
		if errors.Is(err, migrate.ErrAlreadyRunning) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, serverID, "server.migrate_start", map[string]any{"from": srv.MCVersion, "to": target, "run_id": run.ID})
	writeJSON(w, http.StatusAccepted, run)
}

func (h *MigrationHandlers) List(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := h.store.ListVersionMigrations(r.Context(), serverID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*store.VersionMigration{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *MigrationHandlers) Get(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	run, err := h.store.GetVersionMigration(r.Context(), chi.URLParam(r, "runId"))
	if err != nil || run.ServerID != serverID {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}
