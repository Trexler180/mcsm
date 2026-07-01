package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

type FileHandlers struct {
	store *store.Store
}

func NewFileHandlers(s *store.Store) *FileHandlers {
	return &FileHandlers{store: s}
}

func (h *FileHandlers) proxyToAgent(w http.ResponseWriter, r *http.Request, agentSuffix string) {
	id := chi.URLParam(r, "id")
	srv, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	c.ProxyHTTP(ctx, w, r, "/agent/v1/servers/"+srv.ID+agentSuffix)
}

func (h *FileHandlers) List(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files")
}

func (h *FileHandlers) Tree(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/tree")
}

func (h *FileHandlers) GetContent(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/content")
}

func (h *FileHandlers) PutContent(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/content")
}

func (h *FileHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files")
}

func (h *FileHandlers) Rename(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/rename")
}

func (h *FileHandlers) Mkdir(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/mkdir")
}

func (h *FileHandlers) Download(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/download")
}

func (h *FileHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	h.proxyToAgent(w, r, "/files/upload")
}
