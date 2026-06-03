package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/backups"
	"github.com/mcsm/api/internal/store"
)

type BackupHandlers struct {
	store *store.Store
}

func NewBackupHandlers(s *store.Store) *BackupHandlers {
	return &BackupHandlers{store: s}
}

func (h *BackupHandlers) ListBackups(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	backups, err := h.store.ListBackups(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if backups == nil {
		backups = []*store.Backup{}
	}
	writeJSON(w, http.StatusOK, backups)
}

// CreateBackup synchronously asks the agent to zip the server directory and
// records the result. Manual + scheduled backups share this entry point.
func (h *BackupHandlers) CreateBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	claims := auth.ClaimsFrom(r.Context())

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	uid := ""
	if claims != nil {
		uid = claims.UserID
	}
	var triggeredBy *string
	if uid != "" {
		triggeredBy = &uid
	}

	pending := &store.Backup{
		ServerID:    serverID,
		TriggeredBy: triggeredBy,
		Trigger:     "manual",
		Status:      "running",
	}
	created, err := h.store.CreateBackup(r.Context(), pending)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(r.Context(), srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}

	// Run the actual zip in the background so a slow backup doesn't block the
	// HTTP response. Detached context so cancelling the request doesn't kill it.
	go func(b *store.Backup, dir string) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := c.RegisterDir(ctx, b.ServerID, dir); err != nil {
			_ = h.store.UpdateBackupResult(ctx, b.ID, "failed", nil, err.Error())
			return
		}
		result, err := c.Backup(ctx, b.ServerID, b.ID)
		if err != nil {
			_ = h.store.UpdateBackupResult(ctx, b.ID, "failed", nil, err.Error())
			return
		}
		_ = h.store.UpdateBackupResult(ctx, b.ID, "success", &result.SizeBytes, "")
		backups.Enforce(ctx, h.store, b.ServerID)
	}(created, srv.DirectoryPath)

	writeJSON(w, http.StatusCreated, created)
}

// RestoreBackup stops the server, restores the chosen backup on the agent, and
// optionally restarts. Destructive — overwrites the live server directory.
func (h *BackupHandlers) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	backupID := chi.URLParam(r, "backupId")

	backup, err := h.store.GetBackup(r.Context(), backupID)
	if err != nil || backup.ServerID != serverID {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	if backup.Status != "success" {
		writeError(w, http.StatusBadRequest, "can only restore a successful backup")
		return
	}

	srv, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	node, err := h.store.GetNode(r.Context(), srv.NodeID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	if err := c.Restore(ctx, serverID, backupID); err != nil {
		writeError(w, http.StatusBadGateway, "restore failed: "+err.Error())
		return
	}
	_ = h.store.UpdateServerStatus(r.Context(), serverID, "offline")
	audit(h.store, r, serverID, "backup.restore", map[string]any{"backup_id": backupID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (h *BackupHandlers) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	backupID := chi.URLParam(r, "backupId")

	backup, err := h.store.GetBackup(r.Context(), backupID)
	if err != nil || backup.ServerID != serverID {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	if backup.Status == "running" {
		writeError(w, http.StatusConflict, "cannot delete a running backup")
		return
	}

	if backup.Status == "success" {
		srv, err := h.store.GetServer(r.Context(), serverID)
		if err != nil {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		node, err := h.store.GetNode(r.Context(), srv.NodeID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "node not found")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()

		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
			writeError(w, http.StatusBadGateway, "failed to register server directory")
			return
		}
		if err := c.DeleteBackup(ctx, serverID, backupID); err != nil {
			writeError(w, http.StatusBadGateway, "delete failed: "+err.Error())
			return
		}
	}

	if err := h.store.DeleteBackup(r.Context(), backupID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit(h.store, r, serverID, "backup.delete", map[string]any{
		"backup_id": backupID,
		"status":    backup.Status,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *BackupHandlers) ListTargets(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	targets, err := h.store.ListBackupTargets(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if targets == nil {
		targets = []*store.BackupTarget{}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (h *BackupHandlers) CreateTarget(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")

	var body struct {
		Name      string          `json:"name"`
		Type      string          `json:"type"`
		Config    json.RawMessage `json:"config"`
		Retention json.RawMessage `json:"retention"`
		IsDefault bool            `json:"is_default"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" || body.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type required")
		return
	}

	if body.Config == nil {
		body.Config = json.RawMessage("{}")
	}

	t := &store.BackupTarget{
		ServerID:  serverID,
		Name:      body.Name,
		Type:      body.Type,
		Config:    body.Config,
		Retention: body.Retention,
		IsDefault: body.IsDefault,
	}

	created, err := h.store.CreateBackupTarget(r.Context(), t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
