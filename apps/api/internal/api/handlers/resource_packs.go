package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

type ResourcePackHandlers struct {
	store *store.Store
}

type resourcePackSettings struct {
	ResourcePack struct {
		Enabled  bool   `json:"enabled"`
		Path     string `json:"path"`
		PublicID string `json:"public_id"`
		SHA1     string `json:"sha1"`
	} `json:"resource_pack"`
}

func NewResourcePackHandlers(s *store.Store) *ResourcePackHandlers {
	return &ResourcePackHandlers{store: s}
}

func (h *ResourcePackHandlers) Download(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	publicID := chi.URLParam(r, "publicID")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "resource pack not found")
		return
	}

	var settings resourcePackSettings
	if srv.Settings == nil || json.Unmarshal(srv.Settings, &settings) != nil {
		writeError(w, http.StatusNotFound, "resource pack not found")
		return
	}

	pack := settings.ResourcePack
	if !pack.Enabled || pack.Path == "" || pack.PublicID == "" ||
		subtle.ConstantTimeCompare([]byte(pack.PublicID), []byte(publicID)) != 1 ||
		!isPublicResourcePackPath(pack.Path) {
		writeError(w, http.StatusNotFound, "resource pack not found")
		return
	}

	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	proxyReq := r.Clone(ctx)
	proxyReq.Method = http.MethodGet
	proxyReq.URL.RawQuery = "path=" + url.QueryEscape(pack.Path)
	c.ProxyHTTP(ctx, w, proxyReq, "/agent/v1/servers/"+srv.ID+"/files/download")
}

func isPublicResourcePackPath(userPath string) bool {
	clean := path.Clean("/" + userPath)
	if strings.HasPrefix(clean, "/resource-packs/") &&
		strings.EqualFold(path.Ext(clean), ".zip") {
		return true
	}
	return path.Dir(clean) != "/" && strings.EqualFold(path.Base(clean), "resources.zip")
}
