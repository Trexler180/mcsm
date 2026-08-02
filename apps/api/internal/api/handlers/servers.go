package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
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

// withImportSettings merges import metadata (the detected jar + a no-install
// flag) into a server's settings JSON, so Start can run the existing runtime
// without overwriting it. Preserves any other settings the caller sent.
func withImportSettings(settings json.RawMessage, jarFile string) json.RawMessage {
	m := map[string]any{}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &m)
	}
	m["import"] = map[string]any{"jar_file": jarFile, "no_install": true}
	out, err := json.Marshal(m)
	if err != nil {
		return settings
	}
	return out
}

// withoutImportSettings converts a successfully reinstalled imported server
// into a managed runtime while preserving every unrelated settings key.
func withoutImportSettings(settings json.RawMessage) (json.RawMessage, bool) {
	if len(settings) == 0 {
		return settings, false
	}
	m := map[string]any{}
	if err := json.Unmarshal(settings, &m); err != nil {
		return settings, false
	}
	if _, ok := m["import"]; !ok {
		return settings, false
	}
	delete(m, "import")
	out, err := json.Marshal(m)
	if err != nil {
		return settings, false
	}
	return out, true
}

// processDead reports whether an agent status means the JVM has exited.
// crashed and startup_failure are terminal: the agent keeps them (instead of
// offline) so the panel can surface diagnostics, and nothing resets them until
// the next start — so waiting for "offline" on such a server would never end.
func processDead(status any) bool {
	switch status {
	case "offline", "crashed", "startup_failure":
		return true
	}
	return false
}

func stopForRuntimeChange(ctx context.Context, c *agent.Client, serverID string) error {
	info, err := c.GetStatus(ctx, serverID)
	if err != nil {
		return fmt.Errorf("check server status: %w", err)
	}
	if processDead(info["status"]) {
		return nil
	}
	if err := c.StopServer(ctx, serverID, true, 30); err != nil {
		// The process may have exited between the status check and stop request.
		info, statusErr := c.GetStatus(ctx, serverID)
		if statusErr == nil && processDead(info["status"]) {
			return nil
		}
		return fmt.Errorf("stop server: %w", err)
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for server to stop: %w", ctx.Err())
		case <-ticker.C:
			info, err := c.GetStatus(ctx, serverID)
			if err != nil {
				return fmt.Errorf("check stopped server status: %w", err)
			}
			if processDead(info["status"]) {
				return nil
			}
		}
	}
}

// setHelperModSetting records an explicit on/off choice in a server's settings,
// preserving whatever else is stored there.
func setHelperModSetting(settings json.RawMessage, enabled bool) (json.RawMessage, error) {
	m := map[string]any{}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &m); err != nil {
			// Don't destroy settings we failed to parse.
			return nil, err
		}
	}
	m["helper_mod"] = enabled
	return json.Marshal(m)
}

// resolveFolder turns a requested folder id into a value safe to store: nil for
// "ungrouped" (nil or empty string), or the id once it's confirmed to exist.
// Checking here converts what would be a foreign-key 500 into a clear 400.
func (h *ServerHandlers) resolveFolder(r *http.Request, id *string) (*string, error) {
	if id == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*id)
	if trimmed == "" {
		return nil, nil
	}
	if _, err := h.store.GetServerFolder(r.Context(), trimmed); err != nil {
		return nil, fmt.Errorf("folder not found")
	}
	return &trimmed, nil
}

// folderLabel renders a folder id for the audit diff, where "" reads as
// ungrouped.
func folderLabel(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// ImportCandidates lists existing server directories on a node that aren't yet
// managed by the panel, with detected settings to pre-fill the import dialog.
func (h *ServerHandlers) ImportCandidates(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id required")
		return
	}
	c, err := h.agentClient(r.Context(), h.store, nodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	candidates, err := c.ScanImports(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not scan node for servers")
		return
	}

	// Hide directories already managed by a server so the user can't double-import.
	managed := map[string]bool{}
	if servers, err := h.store.ListServers(r.Context()); err == nil {
		for _, s := range servers {
			managed[filepath.Clean(s.DirectoryPath)] = true
		}
	}
	out := make([]agent.ImportCandidate, 0, len(candidates))
	for _, cand := range candidates {
		if managed[filepath.Clean(cand.AbsPath)] {
			continue
		}
		out = append(out, cand)
	}
	writeJSON(w, http.StatusOK, out)
}

// isAdmin reports whether the caller holds the global admin role, read fresh
// from the DB so a demotion takes effect immediately rather than living on in a
// still-valid access token.
func (h *ServerHandlers) isAdmin(r *http.Request) bool {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		return false
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	return err == nil && user.Role == "admin"
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
		writeServerError(w, r, "list servers", err)
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
		FolderID      *string         `json:"folder_id"`
		// ImportExisting adopts a server directory already on disk: its files are
		// left untouched (no EULA write, no runtime install), and JarFile records
		// the existing launcher so start runs their jar rather than fetching one.
		ImportExisting bool   `json:"import_existing"`
		JarFile        string `json:"jar_file"`
	}
	if err := decode(r, &body); err != nil || body.NodeID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "node_id and name are required")
		return
	}
	if body.ImportExisting && strings.TrimSpace(body.DirectoryPath) == "" {
		writeError(w, http.StatusBadRequest, "a server directory is required to import")
		return
	}

	directoryPath, err := resolveServerDirectory(h.serverRoot, body.DirectoryPath, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Never let two servers manage the same directory — that's how an import (or a
	// name collision) would "step on" an existing server.
	if existing, err := h.store.ListServers(r.Context()); err == nil {
		for _, e := range existing {
			if filepath.Clean(e.DirectoryPath) == filepath.Clean(directoryPath) {
				writeError(w, http.StatusConflict, "a server already manages that directory")
				return
			}
		}
	}

	// Record import metadata so start runs the existing runtime as-is.
	if body.ImportExisting {
		body.Settings = withImportSettings(body.Settings, body.JarFile)
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
	// Resolve the folder up front: a bad id should read as a 400 here, not as an
	// opaque foreign-key failure from the insert.
	folderID, err := h.resolveFolder(r, body.FolderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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
		FolderID:      folderID,
	}

	created, err := h.store.CreateServer(r.Context(), srv)
	if err != nil {
		writeServerError(w, r, "create server", err)
		return
	}

	// Best-effort: ask the agent to create the server directory and write
	// eula.txt. Failure here doesn't break server creation — user can fix
	// manually and retry the start. Skipped for imports: the directory already
	// exists and must not be modified (writing eula.txt would step on it).
	if !body.ImportExisting {
		if c, err := h.agentClient(r.Context(), h.store, created.NodeID); err == nil {
			setupCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			_ = c.Setup(setupCtx, created.ID, created.DirectoryPath)
			cancel()
		}
	}

	audit(h.store, r, created.ID, "server.create", map[string]any{"name": created.Name, "platform": created.Platform, "imported": body.ImportExisting})
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
		PublicStatus  *bool           `json:"public_status"`
		PublicSlug    *string         `json:"public_slug"`
		// Raw so the three cases stay distinct: absent leaves the folder alone,
		// explicit null ungroups the server, an id moves it. A *string would
		// collapse the first two into nil.
		FolderID json.RawMessage `json:"folder_id"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// java_binary, jvm_args, and directory_path are start-command inputs the agent
	// executes, so changing them is effectively host code execution. Confine those
	// to global admins — a server-scoped `settings` collaborator must not be able
	// to escalate to running arbitrary binaries/flags on the agent host.
	if body.JavaBinary != nil || body.JVMArgs != nil || body.DirectoryPath != nil {
		if !h.isAdmin(r) {
			writeError(w, http.StatusForbidden, "only an administrator may change java_binary, jvm_args, or directory_path")
			return
		}
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
	if len(body.FolderID) > 0 {
		var target *string
		if err := json.Unmarshal(body.FolderID, &target); err != nil {
			writeError(w, http.StatusBadRequest, "folder_id must be a folder id or null")
			return
		}
		resolved, err := h.resolveFolder(r, target)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		record("folder_id", folderLabel(existing.FolderID), folderLabel(resolved))
		existing.FolderID = resolved
	}
	if body.PublicSlug != nil {
		slug := strings.ToLower(strings.TrimSpace(*body.PublicSlug))
		if slug != "" {
			if err := ValidatePublicSlug(slug); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		record("public_slug", existing.PublicSlug, slug)
		existing.PublicSlug = slug
	}
	if body.PublicStatus != nil {
		record("public_status", existing.PublicStatus, *body.PublicStatus)
		existing.PublicStatus = *body.PublicStatus
	}
	// Enabling the page without a slug would publish nothing reachable —
	// reject so the UI surfaces the missing piece instead of silently no-oping.
	if existing.PublicStatus && existing.PublicSlug == "" {
		writeError(w, http.StatusBadRequest, "public_slug is required while the public status page is enabled")
		return
	}

	if err := h.store.UpdateServer(r.Context(), id, existing); err != nil {
		if strings.Contains(err.Error(), "servers_public_slug") ||
			strings.Contains(err.Error(), "servers.public_slug") {
			writeError(w, http.StatusConflict, "that public URL is already taken by another server")
			return
		}
		writeServerError(w, r, "update server", err)
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
		writeServerError(w, r, "delete server", err)
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

	// One builder for every start path: it carries the imported server's jar and
	// no-install flag, and the helper-mod decision, so none of them can be
	// dropped by forgetting a line here.
	cfg := agent.StartConfigForServer(srv)

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

// Reinstall stops the server, installs the requested runtime, and only then
// commits its version metadata. A successful reinstall also converts an
// imported launcher to managed server.jar semantics.
func (h *ServerHandlers) Reinstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var body struct {
		Platform      string  `json:"platform"`
		MCVersion     string  `json:"mc_version"`
		LoaderVersion *string `json:"loader_version"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	platform := strings.TrimSpace(body.Platform)
	mcVersion := strings.TrimSpace(body.MCVersion)
	loaderVersion := ""
	if body.LoaderVersion != nil {
		loaderVersion = strings.TrimSpace(*body.LoaderVersion)
	}

	// A version change must name the complete target runtime; rejecting partial
	// targets beats silently reinstalling the old version.
	if platform == "" && (mcVersion != "" || loaderVersion != "") {
		writeError(w, http.StatusBadRequest, "platform required when specifying a target version")
		return
	}

	target := *srv
	if platform != "" {
		if mcVersion == "" {
			writeError(w, http.StatusBadRequest, "mc_version required")
			return
		}
		// Only fabric and quilt runtimes take a pinned loader version; accepting
		// one for other platforms would store metadata the install never honors.
		if loaderVersion != "" && platform != "fabric" && platform != "quilt" {
			writeError(w, http.StatusBadRequest, "loader_version is only supported for fabric and quilt")
			return
		}
		target.Platform = platform
		target.MCVersion = mcVersion
		target.LoaderVersion = nil
		if loaderVersion != "" {
			target.LoaderVersion = &loaderVersion
		}
	}

	c, err := h.agentClient(r.Context(), h.store, target.NodeID)
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
	// Confirm the JVM has exited before swapping any runtime artifacts.
	stopCtx, stopCancel := context.WithTimeout(ctx, 45*time.Second)
	if err := stopForRuntimeChange(stopCtx, c, id); err != nil {
		stopCancel()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	stopCancel()

	cfg := map[string]any{
		"directory":   target.DirectoryPath,
		"platform":    target.Platform,
		"mc_version":  target.MCVersion,
		"java_binary": target.JavaBinary,
	}
	if target.LoaderVersion != nil {
		cfg["loader_version"] = *target.LoaderVersion
	}
	if err := c.Reinstall(ctx, id, cfg); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	target.Settings, _ = withoutImportSettings(target.Settings)
	if err := h.store.UpdateServer(r.Context(), id, &target); err != nil {
		writeError(w, http.StatusInternalServerError, "runtime installed but server metadata could not be updated")
		return
	}
	_ = h.store.UpdateServerStatus(r.Context(), id, "offline")
	audit(h.store, r, id, "server.reinstall", map[string]any{
		"platform": target.Platform, "mc_version": target.MCVersion, "loader_version": target.LoaderVersion,
	})
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

	// Send the current configuration rather than letting the agent reuse what it
	// was given at the last start, so a setting changed in the panel since then
	// (the helper mod toggle, for one) is actually applied by this restart.
	cfg := agent.StartConfigForServer(srv)

	if err := c.RestartServer(ctx, id, cfg); err != nil {
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
		writeServerError(w, r, "list log events", err)
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
