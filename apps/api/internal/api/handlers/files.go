package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
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

// worldUploadLimit caps a single world archive. Worlds run large — a
// long-running survival server is comfortably several gigabytes — so this is
// set high enough not to be the thing users hit, while still bounding what one
// request can push at a node.
const worldUploadLimit = int64(16) << 30

// UploadWorld streams a zipped world to the agent, which validates and unpacks
// it into a folder in the server root.
//
// It doesn't go through proxyToAgent because that deliberately runs on a 60s
// deadline: this request can be gigabytes of upload followed by an unpack of
// tens of thousands of region files, so it gets its own budget on both the
// context and the agent client's absolute timeout.
func (h *FileHandlers) UploadWorld(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, worldUploadLimit)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	// serverAgent hands back a client built for this request alone, so raising
	// its ceiling here doesn't affect any other call.
	c.HTTP.Timeout = 2 * time.Hour

	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	// Wrapped so the audit entry records what actually happened: a rejected
	// upload (bad archive, name taken, server still running) is worth seeing in
	// the log as a rejection, not as a successful world replacement.
	rec := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
	c.ProxyHTTP(ctx, rec, r, "/agent/v1/servers/"+srv.ID+"/worlds/upload")
	audit(h.store, r, id, "world.upload", map[string]any{
		"name":      r.URL.Query().Get("name"),
		"overwrite": r.URL.Query().Get("overwrite") == "true",
		"status":    rec.Status(),
	})
}
