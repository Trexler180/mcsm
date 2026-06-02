package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/mcsm/api/internal/mc"
)

type MinecraftHandlers struct {
	client *mc.Client
}

func NewMinecraftHandlers() *MinecraftHandlers {
	return &MinecraftHandlers{client: mc.New()}
}

// Versions lists available Minecraft game versions for a platform.
// GET /api/v1/minecraft/versions?platform=fabric&snapshots=true
func (h *MinecraftHandlers) Versions(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = "vanilla"
	}
	snapshots := r.URL.Query().Get("snapshots") == "true"

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	versions, err := h.client.GameVersions(ctx, platform, snapshots)
	if err != nil {
		writeError(w, http.StatusBadGateway, "version lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// LoaderVersions lists mod-loader versions for fabric/quilt.
// GET /api/v1/minecraft/loaders?platform=fabric
func (h *MinecraftHandlers) LoaderVersions(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		writeError(w, http.StatusBadRequest, "platform required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	versions, err := h.client.LoaderVersions(ctx, platform)
	if err != nil {
		writeError(w, http.StatusBadGateway, "loader lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}
