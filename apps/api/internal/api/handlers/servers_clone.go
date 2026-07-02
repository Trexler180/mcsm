package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// Clone creates a new server from an existing one: same platform, version,
// loader, Java/JVM/RAM configuration and tags, in a fresh directory owned by
// the caller. The runtime is provisioned and the source server's
// source-tracked mods are re-installed in the background. Custom-uploaded jars
// are not copied (their bytes can't be re-fetched from a source index); the
// response reports how many were skipped.
//
// Clone lives on ModHandlers because it reuses installRecursive; it is wired to
// an admin-only route (like Create), which changes host-executed inputs.
func (h *ModHandlers) Clone(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "id")
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		Name          string `json:"name"`
		DirectoryPath string `json:"directory_path"`
		Port          int    `json:"port"`
		CopyMods      *bool  `json:"copy_mods"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	copyMods := body.CopyMods == nil || *body.CopyMods

	src, err := h.store.GetServer(r.Context(), sourceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "source server not found")
		return
	}

	dir, err := resolveServerDirectory(h.serverRoot, body.DirectoryPath, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Never let two servers manage the same directory.
	if existing, err := h.store.ListServers(r.Context()); err == nil {
		for _, e := range existing {
			if e.DirectoryPath == dir {
				writeError(w, http.StatusConflict, "a server already manages that directory")
				return
			}
		}
	}

	port := body.Port
	if port == 0 {
		port = src.Port + 1
	}

	// Copy the config but not import metadata (a clone provisions its own
	// runtime rather than adopting existing files) and not identity/state.
	settings := stripImportSettings(src.Settings)

	clone := &store.Server{
		NodeID:        src.NodeID,
		OwnerID:       claims.UserID,
		Name:          body.Name,
		Description:   src.Description,
		Platform:      src.Platform,
		MCVersion:     src.MCVersion,
		LoaderVersion: src.LoaderVersion,
		DirectoryPath: dir,
		JavaBinary:    src.JavaBinary,
		JVMArgs:       append([]string(nil), src.JVMArgs...),
		Port:          port,
		RAMMbMin:      src.RAMMbMin,
		RAMMbMax:      src.RAMMbMax,
		Tags:          append([]string(nil), src.Tags...),
		Settings:      settings,
	}

	created, err := h.store.CreateServer(r.Context(), clone)
	if err != nil {
		writeServerError(w, r, "clone: create", err)
		return
	}

	// Best-effort directory setup (create dir + eula.txt) like a normal create.
	node, nodeErr := h.store.GetNode(r.Context(), created.NodeID)
	if nodeErr == nil {
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		setupCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		_ = c.Setup(setupCtx, created.ID, created.DirectoryPath)
		cancel()
	}

	sourceMods, _ := h.store.ListMods(r.Context(), sourceID)
	skipped := 0
	var toInstall []*store.InstalledMod
	if copyMods {
		for _, m := range sourceMods {
			// Only mods with a resolvable source + version can be re-fetched.
			if m.SourceID != nil && *m.SourceID != "" && m.VersionID != nil && *m.VersionID != "" {
				toInstall = append(toInstall, m)
			} else {
				skipped++
			}
		}
		if nodeErr == nil && len(toInstall) > 0 {
			go h.installClonedMods(created, node, toInstall)
		}
	}

	audit(h.store, r, created.ID, "server.clone", map[string]any{
		"name": created.Name, "source_id": sourceID,
		"mods_queued": len(toInstall), "mods_skipped": skipped,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"server":       created,
		"mods_queued":  len(toInstall),
		"mods_skipped": skipped,
	})
}

// installClonedMods re-installs the source server's mods onto the clone in the
// background (the HTTP response has already returned). Each install is
// independent; one failure is logged and does not stop the rest.
func (h *ModHandlers) installClonedMods(srv *store.Server, node *store.Node, mods []*store.InstalledMod) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		log.Printf("clone %s: register dir: %v", srv.ID, err)
		return
	}

	for _, m := range mods {
		// Dependencies were tracked on the source; installing each recorded mod
		// directly (deps off) reproduces the exact set without double-counting.
		if _, err := h.installRecursive(ctx, c, srv, m.Source, *m.SourceID, *m.VersionID, false, m.InstalledAsDep, map[string]bool{}); err != nil {
			log.Printf("clone %s: install %s (%s): %v", srv.ID, m.Name, m.Source, err)
		}
	}
	log.Printf("clone %s: finished installing %d mods", srv.ID, len(mods))
}

// stripImportSettings removes the "import" block from a server's settings so a
// clone provisions a fresh runtime instead of adopting on-disk files.
func stripImportSettings(settings json.RawMessage) json.RawMessage {
	if len(settings) == 0 {
		return json.RawMessage("{}")
	}
	m := map[string]any{}
	if err := json.Unmarshal(settings, &m); err != nil {
		return json.RawMessage("{}")
	}
	delete(m, "import")
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}
