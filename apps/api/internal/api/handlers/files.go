package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/api/internal/agent"
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

// maxSyncedPropertiesBytes bounds how much of a server.properties write the API
// will buffer to read the port out of. A real properties file is a couple of
// kilobytes; anything past this is proxied straight through unread rather than
// held in memory.
const maxSyncedPropertiesBytes = 1 << 20

func (h *FileHandlers) PutContent(w http.ResponseWriter, r *http.Request) {
	// Editing server.properties is the other half of the port story. The panel's
	// port is written into that file on setup and before every start, so an
	// operator who edits server-port by hand would otherwise see it reverted at
	// the next launch. Mirror their edit onto the server record instead, and the
	// two stay in agreement whichever side they change.
	if isServerProperties(r.URL.Query().Get("path")) {
		h.putServerProperties(w, r)
		return
	}
	h.proxyToAgent(w, r, "/files/content")
}

// putServerProperties proxies the write like any other file, then adopts the
// server-port it carried as the server's panel port — but only when this write
// is what changed it.
func (h *FileHandlers) putServerProperties(w http.ResponseWriter, r *http.Request) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxSyncedPropertiesBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(buf) > maxSyncedPropertiesBytes {
		// Implausible for this file; hand the whole stream back to the plain proxy
		// rather than buffering the rest of it.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		h.proxyToAgent(w, r, "/files/content")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))

	// Sampled before the write lands, so we can tell an operator changing the
	// port from a write that merely carries it along: the world-upload and
	// resource-pack flows rewrite this whole file to change one unrelated key,
	// and those must not drag the panel's port to whatever the file said.
	id := chi.URLParam(r, "id")
	previous, hadPrevious := h.currentPropertiesPort(r.Context(), id)

	rec := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
	h.proxyToAgent(rec, r, "/files/content")
	if rec.Status() < 200 || rec.Status() >= 300 {
		return
	}

	port, ok := propertiesPort(buf)
	if !ok || (hadPrevious && previous == port) {
		return
	}
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil || srv.Port == port {
		return
	}
	from := srv.Port
	srv.Port = port
	if err := h.store.UpdateServer(r.Context(), id, srv); err != nil {
		return
	}
	audit(h.store, r, id, "server.update", map[string]any{
		"changes": map[string]any{
			"port": map[string]any{"from": from, "to": port, "source": "server.properties"},
		},
	})
}

// currentPropertiesPort reads the server-port that server.properties holds
// right now, reporting ok=false when the file, the key, or the node is
// unavailable — in which case the caller simply doesn't sync.
func (h *FileHandlers) currentPropertiesPort(ctx context.Context, serverID string) (int, bool) {
	srv, err := h.store.GetServer(ctx, serverID)
	if err != nil {
		return 0, false
	}
	node, err := h.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		return 0, false
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.RegisterDir(readCtx, srv.ID, srv.DirectoryPath); err != nil {
		return 0, false
	}
	body, err := c.ReadFile(readCtx, srv.ID, "/server.properties", maxSyncedPropertiesBytes)
	if err != nil {
		return 0, false
	}
	return propertiesPort(body)
}

// isServerProperties reports whether a file-manager path points at the server's
// own server.properties (not, say, a plugin's copy in a subfolder).
func isServerProperties(path string) bool {
	clean := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	return strings.EqualFold(clean, "server.properties")
}

// propertiesPort pulls server-port out of a properties file's contents.
func propertiesPort(body []byte) (int, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(k) != "server-port" {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || port <= 0 || port > 65535 {
			return 0, false
		}
		return port, true
	}
	return 0, false
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
