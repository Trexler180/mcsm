package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type ServerHandlers struct {
	store      *store.Store
	serverRoot string
}

func NewServerHandlers(s *store.Store, serverRoot string) *ServerHandlers {
	return &ServerHandlers{store: s, serverRoot: serverRoot}
}

func (h *ServerHandlers) agentClient(ctx context.Context, s *store.Store, nodeID string) (*agent.Client, error) {
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return agent.New(node.Scheme, node.FQDN, node.Port, node.Token), nil
}

func (h *ServerHandlers) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var (
		servers []*store.Server
		err     error
	)
	if claims.Role == "admin" {
		servers, err = h.store.ListServers(r.Context())
	} else {
		servers, err = h.store.ListServersForUser(r.Context(), claims.UserID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if servers == nil {
		servers = []*store.Server{}
	}
	writeJSON(w, http.StatusOK, servers)
}

func (h *ServerHandlers) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())

	var body struct {
		NodeID        string          `json:"node_id"`
		Name          string          `json:"name"`
		Description   *string         `json:"description"`
		Platform      string          `json:"platform"`
		MCVersion     string          `json:"mc_version"`
		LoaderVersion *string         `json:"loader_version"`
		DirectoryPath string          `json:"directory_path"`
		JavaBinary    string          `json:"java_binary"`
		JVMArgs       []string        `json:"jvm_args"`
		Port          int             `json:"port"`
		RAMMbMin      int             `json:"ram_mb_min"`
		RAMMbMax      int             `json:"ram_mb_max"`
		AutoStart     bool            `json:"auto_start"`
		Tags          []string        `json:"tags"`
		Settings      json.RawMessage `json:"settings"`
	}
	if err := decode(r, &body); err != nil || body.NodeID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "node_id and name are required")
		return
	}

	directoryPath, err := resolveServerDirectory(h.serverRoot, body.DirectoryPath, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if body.Platform == "" {
		body.Platform = "paper"
	}
	if body.MCVersion == "" {
		body.MCVersion = "1.21.4"
	}
	if body.JavaBinary == "" {
		body.JavaBinary = "java"
	}
	if body.Port == 0 {
		body.Port = 25565
	}
	if body.RAMMbMax == 0 {
		body.RAMMbMax = 2048
	}
	if body.RAMMbMin == 0 {
		body.RAMMbMin = 512
	}
	if body.JVMArgs == nil {
		body.JVMArgs = []string{}
	}
	if body.Tags == nil {
		body.Tags = []string{}
	}
	if body.Settings == nil {
		body.Settings = json.RawMessage("{}")
	}

	srv := &store.Server{
		NodeID:        body.NodeID,
		OwnerID:       claims.UserID,
		Name:          body.Name,
		Description:   body.Description,
		Platform:      body.Platform,
		MCVersion:     body.MCVersion,
		LoaderVersion: body.LoaderVersion,
		DirectoryPath: directoryPath,
		JavaBinary:    body.JavaBinary,
		JVMArgs:       body.JVMArgs,
		Port:          body.Port,
		RAMMbMin:      body.RAMMbMin,
		RAMMbMax:      body.RAMMbMax,
		AutoStart:     body.AutoStart,
		Tags:          body.Tags,
		Settings:      body.Settings,
	}

	created, err := h.store.CreateServer(r.Context(), srv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Best-effort: ask the agent to create the server directory and write
	// eula.txt. Failure here doesn't break server creation — user can fix
	// manually and retry the start.
	if c, err := h.agentClient(r.Context(), h.store, created.NodeID); err == nil {
		setupCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		_ = c.Setup(setupCtx, created.ID, created.DirectoryPath)
		cancel()
	}

	audit(h.store, r, created.ID, "server.create", map[string]any{"name": created.Name, "platform": created.Platform})
	writeJSON(w, http.StatusCreated, created)
}

func (h *ServerHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *ServerHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	var body struct {
		Name          *string         `json:"name"`
		Description   *string         `json:"description"`
		Platform      *string         `json:"platform"`
		MCVersion     *string         `json:"mc_version"`
		LoaderVersion *string         `json:"loader_version"`
		DirectoryPath *string         `json:"directory_path"`
		JavaBinary    *string         `json:"java_binary"`
		JVMArgs       []string        `json:"jvm_args"`
		Port          *int            `json:"port"`
		RAMMbMin      *int            `json:"ram_mb_min"`
		RAMMbMax      *int            `json:"ram_mb_max"`
		AutoStart     *bool           `json:"auto_start"`
		Tags          []string        `json:"tags"`
		Settings      json.RawMessage `json:"settings"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Record a before/after diff of the fields the request actually changes, so
	// the audit log can show operators "what changed" rather than a bare event.
	changes := map[string]map[string]any{}
	record := func(field string, from, to any) {
		if from != to {
			changes[field] = map[string]any{"from": from, "to": to}
		}
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	if body.Name != nil {
		record("name", existing.Name, *body.Name)
		existing.Name = *body.Name
	}
	if body.Description != nil {
		record("description", deref(existing.Description), *body.Description)
		existing.Description = body.Description
	}
	if body.Platform != nil {
		record("platform", existing.Platform, *body.Platform)
		existing.Platform = *body.Platform
	}
	if body.MCVersion != nil {
		record("mc_version", existing.MCVersion, *body.MCVersion)
		existing.MCVersion = *body.MCVersion
	}
	if body.LoaderVersion != nil {
		record("loader_version", deref(existing.LoaderVersion), *body.LoaderVersion)
		existing.LoaderVersion = body.LoaderVersion
	}
	if body.DirectoryPath != nil {
		dir, err := resolveServerDirectory(h.serverRoot, *body.DirectoryPath, existing.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		record("directory_path", existing.DirectoryPath, dir)
		existing.DirectoryPath = dir
	}
	if body.JavaBinary != nil {
		record("java_binary", existing.JavaBinary, *body.JavaBinary)
		existing.JavaBinary = *body.JavaBinary
	}
	if body.JVMArgs != nil {
		record("jvm_args", strings.Join(existing.JVMArgs, " "), strings.Join(body.JVMArgs, " "))
		existing.JVMArgs = body.JVMArgs
	}
	if body.Port != nil {
		record("port", existing.Port, *body.Port)
		existing.Port = *body.Port
	}
	if body.RAMMbMin != nil {
		record("ram_mb_min", existing.RAMMbMin, *body.RAMMbMin)
		existing.RAMMbMin = *body.RAMMbMin
	}
	if body.RAMMbMax != nil {
		record("ram_mb_max", existing.RAMMbMax, *body.RAMMbMax)
		existing.RAMMbMax = *body.RAMMbMax
	}
	if body.AutoStart != nil {
		record("auto_start", existing.AutoStart, *body.AutoStart)
		existing.AutoStart = *body.AutoStart
	}
	if body.Tags != nil {
		record("tags", strings.Join(existing.Tags, ", "), strings.Join(body.Tags, ", "))
		existing.Tags = body.Tags
	}
	if body.Settings != nil {
		if !bytes.Equal(existing.Settings, body.Settings) {
			// Avoid dumping the whole config blob; just flag that it changed.
			changes["settings"] = map[string]any{"from": "previous config", "to": "updated config"}
		}
		existing.Settings = body.Settings
	}

	if err := h.store.UpdateServer(r.Context(), id, existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(changes) > 0 {
		audit(h.store, r, id, "server.update", map[string]any{"changes": changes})
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *ServerHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	deleteFiles := r.URL.Query().Get("files") == "true"
	deleteBackups := r.URL.Query().Get("backups") == "true"

	// When the operator opts into disk deletion, purge on the agent *before*
	// dropping the DB row. If the agent is unreachable or the wipe fails we abort
	// and keep the panel record, so the server is never orphaned on disk with no
	// way to find it again. (The backups DB rows cascade-delete with the server.)
	if deleteFiles || deleteBackups {
		srv, err := h.store.GetServer(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "node not found")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
			writeError(w, http.StatusBadGateway, "node unreachable; server not deleted: "+err.Error())
			return
		}
		if err := c.PurgeServer(ctx, srv.ID, srv.DirectoryPath, deleteFiles, deleteBackups); err != nil {
			writeError(w, http.StatusBadGateway, "file deletion failed; server not deleted: "+err.Error())
			return
		}
	}

	if err := h.store.DeleteServer(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit(h.store, r, "", "server.delete", map[string]any{
		"server_id":       id,
		"deleted_files":   deleteFiles,
		"deleted_backups": deleteBackups,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *ServerHandlers) Start(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	cfg := agent.StartConfig(srv.DirectoryPath, srv.JavaBinary, srv.JVMArgs, srv.Platform, srv.MCVersion, srv.RAMMbMin, srv.RAMMbMax)

	// Long deadline because the agent may auto-install the server runtime on
	// first start. Most platforms are fast (~10–60s), Spigot BuildTools can
	// take 10+ minutes since it compiles from source.
	//
	// This is intentionally detached from r.Context(): refreshing or closing the
	// browser tab should not cancel a server start that is already installing or
	// booting on the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
	defer cancel()
	setStatus := func(status string) {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer statusCancel()
		_ = h.store.UpdateServerStatus(statusCtx, id, status)
	}

	// Persist the intent before the long agent call. First-time starts can spend
	// minutes auto-installing a runtime before the agent process exists, and a
	// page refresh should still show the server as starting.
	setStatus("starting")
	if err := c.StartServer(ctx, id, cfg); err != nil {
		setStatus("offline")
		// Surface the failed start as a cockpit signal, not just an HTTP error.
		audit(h.store, r, id, "server.start_failed", map[string]any{"error": err.Error()})
		_ = h.store.InsertLogEvent(r.Context(), id, "error", "Server failed to start: "+err.Error(), "api")
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	setStatus("starting")
	audit(h.store, r, id, "server.start", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

// JavaInstallations proxies the server's node agent for the Java runtimes
// installed on that host, so the panel can offer to switch a server to a
// compatible version (or suggest installing one) after a Java-version crash.
func (h *ServerHandlers) JavaInstallations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}
	info, err := c.JavaInstallations(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// Reinstall stops the server and re-fetches its runtime jar for the currently
// configured platform/version, so a version change actually applies.
func (h *ServerHandlers) Reinstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 16*time.Minute)
	defer cancel()

	if err := c.RegisterDir(ctx, id, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	// Stop first so we don't swap the jar under a running process.
	_ = c.StopServer(ctx, id, true, 30)

	cfg := map[string]any{
		"directory":   srv.DirectoryPath,
		"platform":    srv.Platform,
		"mc_version":  srv.MCVersion,
		"java_binary": srv.JavaBinary,
	}
	if err := c.Reinstall(ctx, id, cfg); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.store.UpdateServerStatus(r.Context(), id, "offline")
	audit(h.store, r, id, "server.reinstall", map[string]any{"platform": srv.Platform, "mc_version": srv.MCVersion})
	writeJSON(w, http.StatusOK, map[string]string{"status": "reinstalled"})
}

func (h *ServerHandlers) Stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	var body struct {
		Graceful   bool `json:"graceful"`
		TimeoutSec int  `json:"timeout_sec"`
	}
	body.Graceful = true
	body.TimeoutSec = 30
	_ = decode(r, &body)

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(body.TimeoutSec+5)*time.Second)
	defer cancel()

	if err := c.StopServer(ctx, id, body.Graceful, body.TimeoutSec); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.store.UpdateServerStatus(r.Context(), id, "stopping")
	audit(h.store, r, id, "server.stop", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (h *ServerHandlers) Restart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if err := c.RestartServer(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(h.store, r, id, "server.restart", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func (h *ServerHandlers) Kill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := c.KillServer(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.store.UpdateServerStatus(r.Context(), id, "offline")
	audit(h.store, r, id, "server.kill", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "offline"})
}

// LogEvents returns recent indexed warnings/errors for a server. Filter with
// ?level=error and cap with ?limit=N.
func (h *ServerHandlers) LogEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	level := r.URL.Query().Get("level")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.store.ListLogEvents(r.Context(), id, level, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *ServerHandlers) Status(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, err := c.GetStatus(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "offline"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *ServerHandlers) Command(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	var body struct {
		Command string `json:"command"`
	}
	if err := decode(r, &body); err != nil || body.Command == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := c.SendCommand(ctx, id, body.Command); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
