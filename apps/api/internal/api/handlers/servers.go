package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/api/apierr"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/lifecycle"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

type ServerHandlers struct {
	store      *store.Store
	serverRoot string
	life       *lifecycle.Service
}

// NewServerHandlers builds the server handlers with a private lifecycle
// service; the router shares one with the scheduler and MCP via WithLifecycle.
func NewServerHandlers(s *store.Store, serverRoot string) *ServerHandlers {
	return &ServerHandlers{
		store:      s,
		serverRoot: serverRoot,
		life:       lifecycle.New(s, operations.New(s, nil), nil, nil),
	}
}

// WithLifecycle shares a lifecycle service (and with it the operation lock and
// event bus) with the rest of the panel.
func (h *ServerHandlers) WithLifecycle(life *lifecycle.Service) *ServerHandlers {
	h.life = life
	return h
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

// withoutImportSettings drops the import marker once a reinstall has replaced
// an imported runtime with a managed one, reporting whether it changed.
func withoutImportSettings(settings json.RawMessage) (json.RawMessage, bool) {
	out := lifecycle.WithoutImportSettings(settings)
	return out, string(out) != string(settings)
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
//
// A machine principal is never an administrator, however privileged the human
// who owns the key. The only field-level gate in this file — java_binary,
// jvm_args, directory_path — stands between a `settings`-scoped key and code
// execution on the agent host, so admin ownership must not satisfy it.
func (h *ServerHandlers) isAdmin(r *http.Request) bool {
	if auth.MachineFrom(r.Context()) != nil {
		return false
	}
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

	isAdmin, err := callerIsAdmin(r, h.store)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var servers []*store.Server
	if isAdmin {
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

	// Attach the caller's effective permissions on each server so the panel can
	// gate per-server actions (power buttons, section links) without a
	// members/me round trip per row. Admins and owners resolve to the full set,
	// mirroring requireServerPermission's bypasses.
	granted := map[string][]string{}
	if !isAdmin {
		granted, err = h.store.AllServerPermissionsForUser(r.Context(), claims.UserID)
		if err != nil {
			writeServerError(w, r, "list server permissions", err)
			return
		}
	}

	// An agent access key sees the intersection of its allowlist with what its
	// owner can currently reach — never the names or ids of the rest of the
	// fleet, even when the owner is a global admin.
	machine := auth.MachineFrom(r.Context())

	out := make([]serverListItem, 0, len(servers))
	for _, srv := range servers {
		if machine != nil && !machine.AllowsServer(srv.ID) {
			continue
		}
		perms := granted[srv.ID]
		if isAdmin || srv.OwnerID == claims.UserID {
			perms = store.AllServerPermissions()
		}
		if machine != nil {
			// Report what the key can actually do here, not what its owner
			// could do by hand — the same intersection the access gate applies.
			perms = intersectWithScopes(perms, machine.Scopes)
		}
		if perms == nil {
			perms = []string{}
		}
		out = append(out, serverListItem{serverView: viewServer(srv, isAdmin && machine == nil), Permissions: perms})
	}
	writeJSON(w, http.StatusOK, out)
}

// intersectWithScopes reports what a key can actually do: the greatest lower
// bound of the key's scopes and its owner's live grants in the permission
// lattice, expressed as the set of permissions the route gate would actually
// allow. It walks the key's own scopes — the narrower side, and the side the
// gate checks first — and narrows each one against the grants.
//
// Two cases per scope, and no third:
//
//   - The grants satisfy the scope outright (store.HasServerPermission: the
//     leaf itself, its parent group, or admin). The scope survives as-is, so an
//     owner's `power` group plus a key's `power.restart` leaf reports
//     `power.restart` rather than the group the key does not have.
//   - They do not, and the scope is a group. The overlap is then whatever leaves
//     of that group the owner holds: a `power` scope against an owner holding
//     only `power.restart` reports `power.restart`, which is exactly what the
//     gate lets through — the scope side satisfies the leaf via its parent
//     group, and the user side satisfies it directly.
//
// Anything else — a leaf scope the grants do not cover, or a group with no
// overlapping leaf — contributes nothing. Narrowing never widens: every
// permission emitted is one both sides already satisfy, so the report can only
// name authority the gate would honor.
func intersectWithScopes(perms, scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := make(map[string]bool, len(scopes))
	keep := func(perm string) {
		if !seen[perm] {
			seen[perm] = true
			out = append(out, perm)
		}
	}
	for _, scope := range scopes {
		if store.HasServerPermission(perms, store.ServerPermission(scope)) {
			keep(scope)
			continue
		}
		if strings.Contains(scope, ".") {
			continue // a leaf the owner does not hold: no narrower form exists
		}
		// A group scope the owner does not hold outright still overlaps with any
		// leaf of that group they do hold.
		prefix := scope + "."
		for _, perm := range perms {
			if strings.HasPrefix(perm, prefix) {
				keep(perm)
			}
		}
	}
	return out
}

// serverListItem is a server plus the requesting user's effective permissions on
// it. The embedded pointer flattens into the same JSON shape the panel already
// reads, so `permissions` is purely additive.
type serverListItem struct {
	serverView
	Permissions []string `json:"permissions"`
}

// serverView is a server as one caller may see it. directory_path,
// java_binary and jvm_args are the agent's start command — where it runs and
// what it executes — so only administrators read them (as only they may
// change them); for everyone else they are null. The fields here shadow the
// embedded server's.
type serverView struct {
	*store.Server
	DirectoryPath *string  `json:"directory_path"`
	JavaBinary    *string  `json:"java_binary"`
	JVMArgs       []string `json:"jvm_args"`
	// Isolated says whether the server's node runs it as the dedicated server
	// user (null when the node has not reported). Without it, whoever may
	// write its files or install its mods can run code as the panel's user.
	Isolated *bool `json:"isolated,omitempty"`
}

func viewServer(srv *store.Server, isAdmin bool) serverView {
	v := serverView{Server: srv}
	if isAdmin {
		v.DirectoryPath = &srv.DirectoryPath
		v.JavaBinary = &srv.JavaBinary
		v.JVMArgs = srv.JVMArgs
		if v.JVMArgs == nil {
			v.JVMArgs = []string{}
		}
	}
	return v
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
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.NodeID == "" {
		writeFieldError(w, "node_id", "node_id is required")
		return
	}
	name, err := validateServerName(body.Name)
	if err != nil {
		respondInvalid(w, err)
		return
	}
	body.Name = name
	node, err := h.store.GetNode(r.Context(), body.NodeID)
	if err != nil {
		writeFieldError(w, "node_id", "node not found")
		return
	}
	if body.ImportExisting && strings.TrimSpace(body.DirectoryPath) == "" {
		writeFieldError(w, "directory_path", "a server directory is required to import")
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
	for _, check := range []error{
		validatePlatform(body.Platform),
		validateVersion("mc_version", body.MCVersion),
		validatePort(body.Port),
		validateRAM(body.RAMMbMin, body.RAMMbMax, node),
		validateJarFile(body.JarFile),
		validateTags(body.Tags),
		validateDescription(body.Description),
	} {
		if check != nil {
			respondInvalid(w, check)
			return
		}
	}
	if body.LoaderVersion != nil && strings.TrimSpace(*body.LoaderVersion) != "" {
		if err := validateVersion("loader_version", strings.TrimSpace(*body.LoaderVersion)); err != nil {
			respondInvalid(w, err)
			return
		}
	}

	directoryPath, err := resolveServerDirectory(h.serverRoot, body.DirectoryPath, body.Name)
	if err != nil {
		writeFieldError(w, "directory_path", err.Error())
		return
	}

	// Never let two servers manage the same directory, or one inside another's —
	// that's how an import (or a name collision) would "step on" an existing
	// server, and nested directories would let one server's file manager reach
	// into the other.
	if existing, err := h.store.ListServers(r.Context()); err == nil {
		for _, e := range existing {
			if dirsOverlap(e.DirectoryPath, directoryPath) {
				writeCodedError(w, http.StatusConflict, "conflict", fmt.Sprintf("that directory overlaps %q's", e.Name))
				return
			}
		}
	}
	// Record import metadata so start runs the existing runtime as-is.
	if body.ImportExisting {
		body.Settings = withImportSettings(body.Settings, body.JarFile)
	}
	if body.Settings == nil {
		body.Settings = json.RawMessage("{}")
	}
	settings, err := validateSettingsChange(nil, body.Settings, true)
	if err != nil {
		respondInvalid(w, err)
		return
	}
	if body.JVMArgs == nil {
		body.JVMArgs = []string{}
	}
	if body.Tags == nil {
		body.Tags = []string{}
	}
	// Resolve the folder up front: a bad id should read as a 400 here, not as an
	// opaque foreign-key failure from the insert.
	folderID, err := h.resolveFolder(r, body.FolderID)
	if err != nil {
		writeFieldError(w, "folder_id", err.Error())
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
		Settings:      settings,
		FolderID:      folderID,
	}

	created, err := h.store.CreateServer(r.Context(), srv)
	if err != nil {
		writeServerError(w, r, "create server", err)
		return
	}

	// Best-effort: ask the agent to create the server directory, write eula.txt,
	// and seed server.properties with the chosen port. Failure here doesn't break
	// server creation — the user can fix it manually, and the port is applied
	// again at every start. Skipped for imports: the directory already exists and
	// must not be modified (writing eula.txt would step on it).
	if !body.ImportExisting {
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		setupCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		_ = c.Setup(setupCtx, created.ID, created.DirectoryPath, created.Port)
		cancel()
	}

	audit(h.store, r, created.ID, "server.create", map[string]any{"name": created.Name, "platform": created.Platform, "imported": body.ImportExisting})
	writeJSON(w, http.StatusCreated, viewServer(created, h.isAdmin(r)))
}

// dirsOverlap reports whether two server directories are the same or one
// contains the other.
func dirsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

func (h *ServerHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	view := viewServer(srv, h.isAdmin(r))
	if node, err := h.store.GetNode(r.Context(), srv.NodeID); err == nil && len(node.Isolation) > 0 {
		var iso struct {
			Active bool `json:"active"`
		}
		if json.Unmarshal(node.Isolation, &iso) == nil {
			view.Isolated = &iso.Active
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *ServerHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
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
	isAdmin := h.isAdmin(r)

	// java_binary, jvm_args, and directory_path are start-command inputs the agent
	// executes, so changing them is effectively host code execution. Confine those
	// to global admins — a server-scoped `settings` collaborator must not be able
	// to escalate to running arbitrary binaries/flags on the agent host.
	hostChange := (body.JavaBinary != nil && *body.JavaBinary != existing.JavaBinary) ||
		(body.JVMArgs != nil && !slices.Equal(body.JVMArgs, existing.JVMArgs)) ||
		(body.DirectoryPath != nil && *body.DirectoryPath != existing.DirectoryPath)
	if hostChange {
		if !isAdmin {
			writeCodedError(w, http.StatusForbidden, "forbidden", "only an administrator may change java_binary, jvm_args, or directory_path")
			return
		}
	}
	// The version fields describe the runtime on disk. Changing them here
	// installs nothing, so the record would stop matching the server; Reinstall
	// and Migrate change both together. An administrator may still correct the
	// record by hand (after upgrading a server outside the panel, say).
	versionChange := (body.Platform != nil && *body.Platform != existing.Platform) ||
		(body.MCVersion != nil && *body.MCVersion != existing.MCVersion) ||
		(body.LoaderVersion != nil && *body.LoaderVersion != deref(existing.LoaderVersion))
	if versionChange && !isAdmin {
		writeFieldError(w, "mc_version", "use Reinstall or Migrate to change the server's software or version")
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

	if body.Name != nil {
		name, err := validateServerName(*body.Name)
		if err != nil {
			respondInvalid(w, err)
			return
		}
		record("name", existing.Name, name)
		existing.Name = name
	}
	if body.Description != nil {
		if err := validateDescription(body.Description); err != nil {
			respondInvalid(w, err)
			return
		}
		record("description", deref(existing.Description), *body.Description)
		existing.Description = body.Description
	}
	if body.Platform != nil {
		if err := validatePlatform(*body.Platform); err != nil {
			respondInvalid(w, err)
			return
		}
		record("platform", existing.Platform, *body.Platform)
		existing.Platform = *body.Platform
	}
	if body.MCVersion != nil {
		if err := validateVersion("mc_version", *body.MCVersion); err != nil {
			respondInvalid(w, err)
			return
		}
		record("mc_version", existing.MCVersion, *body.MCVersion)
		existing.MCVersion = *body.MCVersion
	}
	if body.LoaderVersion != nil {
		if v := strings.TrimSpace(*body.LoaderVersion); v != "" {
			if err := validateVersion("loader_version", v); err != nil {
				respondInvalid(w, err)
				return
			}
		}
		record("loader_version", deref(existing.LoaderVersion), *body.LoaderVersion)
		existing.LoaderVersion = body.LoaderVersion
	}
	if body.DirectoryPath != nil {
		dir, err := resolveServerDirectory(h.serverRoot, *body.DirectoryPath, existing.Name)
		if err != nil {
			writeFieldError(w, "directory_path", err.Error())
			return
		}
		if servers, err := h.store.ListServers(r.Context()); err == nil {
			for _, other := range servers {
				if other.ID != existing.ID && dirsOverlap(other.DirectoryPath, dir) {
					writeCodedError(w, http.StatusConflict, "conflict", fmt.Sprintf("that directory overlaps %q's", other.Name))
					return
				}
			}
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
	portChanged := false
	if body.Port != nil {
		if err := validatePort(*body.Port); err != nil {
			respondInvalid(w, err)
			return
		}
		// Servers may share a port; starting one while another runs on it is
		// what's refused (see lifecycle.PortInUseError).
		portChanged = *body.Port != existing.Port
		record("port", existing.Port, *body.Port)
		existing.Port = *body.Port
	}
	if body.RAMMbMin != nil || body.RAMMbMax != nil {
		minMb, maxMb := existing.RAMMbMin, existing.RAMMbMax
		if body.RAMMbMin != nil {
			minMb = *body.RAMMbMin
		}
		if body.RAMMbMax != nil {
			maxMb = *body.RAMMbMax
		}
		node, _ := h.store.GetNode(r.Context(), existing.NodeID)
		if err := validateRAM(minMb, maxMb, node); err != nil {
			respondInvalid(w, err)
			return
		}
		record("ram_mb_min", existing.RAMMbMin, minMb)
		record("ram_mb_max", existing.RAMMbMax, maxMb)
		existing.RAMMbMin, existing.RAMMbMax = minMb, maxMb
	}
	if body.AutoStart != nil {
		record("auto_start", existing.AutoStart, *body.AutoStart)
		existing.AutoStart = *body.AutoStart
	}
	if body.Tags != nil {
		if err := validateTags(body.Tags); err != nil {
			respondInvalid(w, err)
			return
		}
		record("tags", strings.Join(existing.Tags, ", "), strings.Join(body.Tags, ", "))
		existing.Tags = body.Tags
	}
	if body.Settings != nil {
		settings, err := validateSettingsChange(existing.Settings, body.Settings, isAdmin)
		if err != nil {
			if fe, ok := asFieldError(err); ok && strings.HasPrefix(fe.Msg, "only an administrator") {
				writeCodedError(w, http.StatusForbidden, "forbidden", fe.Msg)
				return
			}
			respondInvalid(w, err)
			return
		}
		if !jsonEqual(existing.Settings, settings) {
			// Avoid dumping the whole config blob; just flag that it changed.
			changes["settings"] = map[string]any{"from": "previous config", "to": "updated config"}
		}
		existing.Settings = settings
	}
	if len(body.FolderID) > 0 {
		var target *string
		if err := json.Unmarshal(body.FolderID, &target); err != nil {
			writeFieldError(w, "folder_id", "folder_id must be a folder id or null")
			return
		}
		resolved, err := h.resolveFolder(r, target)
		if err != nil {
			writeFieldError(w, "folder_id", err.Error())
			return
		}
		record("folder_id", folderLabel(existing.FolderID), folderLabel(resolved))
		existing.FolderID = resolved
	}
	if body.PublicSlug != nil {
		slug := strings.ToLower(strings.TrimSpace(*body.PublicSlug))
		if slug != "" {
			if err := ValidatePublicSlug(slug); err != nil {
				writeFieldError(w, "public_slug", err.Error())
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
		writeFieldError(w, "public_slug", "public_slug is required while the public status page is enabled")
		return
	}

	if err := h.store.UpdateServer(r.Context(), id, existing); err != nil {
		if strings.Contains(err.Error(), "servers_public_slug") ||
			strings.Contains(err.Error(), "servers.public_slug") {
			writeCodedError(w, http.StatusConflict, "conflict", "that public URL is already taken by another server")
			return
		}
		writeServerError(w, r, "update server", err)
		return
	}

	// Push a new port straight into server.properties so the file and the panel
	// agree immediately — otherwise the properties tab would keep showing the old
	// port until the next start. Best-effort: the port is applied again at every
	// launch, so an unreachable node here delays the change rather than losing it.
	if portChanged && existing.Port > 0 {
		if c, err := h.agentClient(r.Context(), h.store, existing.NodeID); err == nil {
			portCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			_ = c.ApplyPort(portCtx, existing.ID, existing.DirectoryPath, existing.Port)
			cancel()
		}
	}

	if len(changes) > 0 {
		audit(h.store, r, id, "server.update", map[string]any{"changes": changes})
	}
	writeJSON(w, http.StatusOK, viewServer(existing, isAdmin))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (h *ServerHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	q := r.URL.Query()
	err = h.life.Delete(r.Context(), srv, h.actor(r), lifecycle.DeleteOptions{
		Files:   q.Get("files") == "true",
		Backups: q.Get("backups") == "true",
		Force:   q.Get("force") == "true",
	})
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// actor attributes a lifecycle action to the caller.
func (h *ServerHandlers) actor(r *http.Request) operations.Actor {
	return operations.ActorFromRequest(r, clientIP(r))
}

// Start launches the server as an operation and answers at once with it:
// installing a runtime first can take many minutes, and the start must not
// depend on the browser staying on the page. Follow the operation (or the
// server's status) for the outcome.
func (h *ServerHandlers) Start(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	op, err := h.life.Start(r.Context(), srv, h.actor(r))
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "starting", "operation": op})
}

// JavaInstallations proxies the server's node agent for the Java runtimes
// installed on that host, so the panel can offer to switch a server to a
// compatible version (or suggest installing one) after a Java-version crash.
func (h *ServerHandlers) JavaInstallations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeCodedError(w, http.StatusBadGateway, "node_unreachable", "node not found")
		return
	}
	info, err := c.JavaInstallations(r.Context())
	if err != nil {
		writeCodedError(w, http.StatusBadGateway, "node_unreachable", "the node could not be reached")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// Reinstall installs the requested runtime as an operation and answers at once
// with it. A successful reinstall also converts an imported launcher to
// managed server.jar semantics.
func (h *ServerHandlers) Reinstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
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
		writeFieldError(w, "platform", "platform required when specifying a target version")
		return
	}
	target := lifecycle.ReinstallTarget{}
	if platform != "" {
		if mcVersion == "" {
			writeFieldError(w, "mc_version", "mc_version required")
			return
		}
		for _, check := range []error{validatePlatform(platform), validateVersion("mc_version", mcVersion)} {
			if check != nil {
				respondInvalid(w, check)
				return
			}
		}
		if loaderVersion != "" {
			if err := validateVersion("loader_version", loaderVersion); err != nil {
				respondInvalid(w, err)
				return
			}
		}
		// Only fabric and quilt runtimes take a pinned loader version; accepting
		// one for other platforms would store metadata the install never honors.
		if loaderVersion != "" && platform != "fabric" && platform != "quilt" {
			writeFieldError(w, "loader_version", "loader_version is only supported for fabric and quilt")
			return
		}
		target.Platform, target.MCVersion = platform, mcVersion
		if loaderVersion != "" {
			target.LoaderVersion = &loaderVersion
		}
	}

	op, err := h.life.Reinstall(r.Context(), srv, target, h.actor(r))
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "reinstalling", "operation": op})
}

func (h *ServerHandlers) Stop(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	body := struct {
		Graceful   bool `json:"graceful"`
		TimeoutSec int  `json:"timeout_sec"`
	}{Graceful: true, TimeoutSec: 30}
	_ = decode(r, &body)
	if err := h.life.Stop(r.Context(), srv, h.actor(r), lifecycle.StopOptions{Graceful: body.Graceful, TimeoutSec: body.TimeoutSec}); err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (h *ServerHandlers) Restart(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	if err := h.life.Restart(r.Context(), srv, h.actor(r)); err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

// Kill ends the process at once. ?force=true also cancels an operation that
// holds the server; without it, a held server refuses.
func (h *ServerHandlers) Kill(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	if err := h.life.Kill(r.Context(), srv, h.actor(r), r.URL.Query().Get("force") == "true"); err != nil {
		writeLifecycleError(w, r, err)
		return
	}
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

// Status reports the server's live state from its node. When the node cannot
// be reached the answer says so — "unknown", with node_reachable false —
// rather than claiming the server is offline.
func (h *ServerHandlers) Status(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	unknown := map[string]any{"id": id, "status": "unknown", "node_reachable": false, "last_known_status": srv.Status}
	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeJSON(w, http.StatusOK, unknown)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := c.GetStatus(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, unknown)
		return
	}
	status["node_reachable"] = true
	if op, err := h.store.RunningOperation(r.Context(), id); err == nil && op != nil {
		status["operation"] = op
	}
	writeJSON(w, http.StatusOK, status)
}

// maxConsoleCommandLength bounds a console command, which also bounds what the
// audit log stores of it.
const maxConsoleCommandLength = 1000

func (h *ServerHandlers) Command(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}

	var body struct {
		Command string `json:"command"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Command) == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return
	}
	// One command per request: a newline would smuggle a second one past
	// whatever reads the audit log.
	if strings.ContainsAny(body.Command, "\r\n") || len(body.Command) > maxConsoleCommandLength {
		writeFieldError(w, "command", "a command is one line of at most 1000 characters")
		return
	}

	c, err := h.agentClient(r.Context(), h.store, srv.NodeID)
	if err != nil {
		writeCodedError(w, http.StatusBadGateway, "node_unreachable", "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := c.SendCommand(ctx, id, body.Command); err != nil {
		writeCodedError(w, http.StatusBadGateway, "agent_error", err.Error())
		return
	}
	// A delegated (MCP) command is recorded by the facade, with the verb it
	// was rendered from; recording it here too would log it twice.
	if auth.DelegatedActorFrom(r.Context()) == nil {
		audit(h.store, r, id, "console.command", map[string]any{"command": body.Command})
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// writeLifecycleError maps a lifecycle failure to a response: a held server is
// a 409 naming the operation, an unreachable node a 502, anything else the
// node's own refusal.
func writeLifecycleError(w http.ResponseWriter, r *http.Request, err error) {
	if busy, ok := store.AsServerBusy(err); ok {
		// operation is kept at the top level for clients written against it,
		// and also carried in details like every other envelope.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     busy.Error(),
			"code":      "server_busy",
			"operation": busy.Current,
			"details":   map[string]any{"operation": busy.Current},
			"retryable": true,
		})
		return
	}
	var portBusy *lifecycle.PortInUseError
	if errors.As(err, &portBusy) {
		writeCodedError(w, http.StatusConflict, "port_in_use", err.Error())
		return
	}
	if errors.Is(err, lifecycle.ErrNodeUnreachable) {
		apierr.WriteBody(w, http.StatusBadGateway, apierr.Body{Error: err.Error(), Code: "node_unreachable", Retryable: true})
		return
	}
	if err.Error() == "node not found" {
		writeCodedError(w, http.StatusBadGateway, "node_unreachable", "the server's node is not registered")
		return
	}
	apierr.WriteBody(w, http.StatusBadGateway, apierr.Body{Error: err.Error(), Code: "agent_error"})
}
