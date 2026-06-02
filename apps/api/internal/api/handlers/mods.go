package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

type ModHandlers struct {
	store    *store.Store
	modrinth *modrinth.Client
}

func NewModHandlers(s *store.Store) *ModHandlers {
	return &ModHandlers{store: s, modrinth: modrinth.New()}
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
		Query     string `json:"query"`
		Loader    string `json:"loader"`
		MCVersion string `json:"mc_version"`
		Limit     int    `json:"limit"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := h.modrinth.Search(ctx, body.Query, body.Loader, body.MCVersion, body.Limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "modrinth search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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

	versions, err := h.modrinth.GetVersions(ctx, projectID, loader, mcVersion)
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

	project, err := h.modrinth.GetProject(ctx, projectID)
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

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Resolve specific version or latest compatible
	var ver *modrinth.Version
	if body.VersionID != "" {
		ver, err = h.modrinth.GetVersion(ctx, body.VersionID)
	} else {
		loader := ""
		mcVer := srv.MCVersion
		if srv.LoaderVersion != nil {
			loader = srv.Platform
		}
		versions, e := h.modrinth.GetVersions(ctx, body.ProjectID, loader, mcVer)
		if e != nil || len(versions) == 0 {
			writeError(w, http.StatusBadRequest, "no compatible version found")
			return
		}
		ver = &versions[0]
		err = nil
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "version lookup failed: "+err.Error())
		return
	}

	// Find primary file
	var primaryFile *modrinth.VersionFile
	for i := range ver.Files {
		if ver.Files[i].Primary {
			primaryFile = &ver.Files[i]
			break
		}
	}
	if primaryFile == nil && len(ver.Files) > 0 {
		primaryFile = &ver.Files[0]
	}
	if primaryFile == nil {
		writeError(w, http.StatusBadRequest, "no files in version")
		return
	}

	// Download JAR from Modrinth CDN
	dlResp, err := http.Get(primaryFile.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer dlResp.Body.Close()

	fileData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read failed")
		return
	}

	// Build multipart body for agent upload
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", primaryFile.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "multipart error")
		return
	}
	fw.Write(fileData)
	mw.Close()

	// Get agent client
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	uploadURL := fmt.Sprintf("%s/agent/v1/servers/%s/files/upload?path=%s",
		c.BaseURL, serverID, url.QueryEscape("/mods"))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	uploadResp, err := c.HTTP.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upload to agent failed: "+err.Error())
		return
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode >= 400 {
		writeError(w, http.StatusBadGateway, "agent upload failed")
		return
	}

	// Record in DB
	sha := primaryFile.Hashes.SHA256
	pid := body.ProjectID
	vid := ver.ID
	mod := &store.InstalledMod{
		ServerID:  serverID,
		Source:    "modrinth",
		SourceID:  &pid,
		VersionID: &vid,
		Name:      ver.Name,
		Version:   ver.VersionNumber,
		FileName:  primaryFile.Filename,
		SHA256:    &sha,
	}

	created, err := h.store.CreateMod(r.Context(), mod)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *ModHandlers) Uninstall(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	modID := chi.URLParam(r, "modId")

	mod, err := h.store.GetMod(r.Context(), modID)
	if err != nil {
		writeError(w, http.StatusNotFound, "mod not found")
		return
	}
	if mod.ServerID != serverID {
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	delURL := fmt.Sprintf("%s/agent/v1/servers/%s/files?path=%s",
		c.BaseURL, serverID,
		url.QueryEscape("/mods/"+mod.FileName))

	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, _ := c.HTTP.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	if _, err := h.store.DeleteMod(r.Context(), modID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
