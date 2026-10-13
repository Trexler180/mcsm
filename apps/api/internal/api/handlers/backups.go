package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/backups"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

type BackupHandlers struct {
	store *store.Store
	svc   *backups.Service
}

// NewBackupHandlers builds the backup handlers with a private service; the
// router shares one (and its operation lock) via WithService.
func NewBackupHandlers(s *store.Store, notifier *notify.Engine) *BackupHandlers {
	return &BackupHandlers{store: s, svc: backups.NewService(s, operations.New(s, nil), nil, notifier, nil)}
}

// WithService shares a backup service with the rest of the panel.
func (h *BackupHandlers) WithService(svc *backups.Service) *BackupHandlers {
	h.svc = svc
	return h
}

func (h *BackupHandlers) ListBackups(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	list, err := h.store.ListBackups(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "list backups", err)
		return
	}
	if list == nil {
		list = []*store.Backup{}
	}
	writeJSON(w, http.StatusOK, list)
}

// backupStatus is the safe polling projection used by delegated operator tools.
// Raw metadata can contain backend error strings, paths, or infrastructure
// details and is intentionally excluded.
type backupStatus struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	SizeBytes   *int64     `json:"size_bytes,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func (h *BackupHandlers) ListBackupsSafe(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	records, err := h.store.ListBackups(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "list backups", err)
		return
	}
	out := make([]backupStatus, 0, len(records))
	for _, record := range records {
		out = append(out, backupStatus{
			ID: record.ID, Status: record.Status, SizeBytes: record.SizeBytes,
			StartedAt: record.StartedAt, CompletedAt: record.CompletedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *BackupHandlers) actor(r *http.Request) operations.Actor {
	return operations.ActorFromRequest(r, clientIP(r))
}

// createdBackup is a new backup record plus the operation producing it.
type createdBackup struct {
	*store.Backup
	Operation *store.Operation `json:"operation"`
}

// CreateBackup starts a backup as an operation and answers with the (still
// running) record at once. While it runs, the server is held: a scheduled
// restart cannot land mid-archive.
func (h *BackupHandlers) CreateBackup(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	b, op, err := h.svc.Create(r.Context(), srv, h.actor(r), "manual")
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createdBackup{Backup: b, Operation: op})
}

// RestoreBackup restores a backup as an operation, after first backing up the
// current world (unless ?safety_backup=false), and answers at once.
func (h *BackupHandlers) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	op, err := h.svc.Restore(r.Context(), srv, chi.URLParam(r, "backupId"), h.actor(r), backups.RestoreOptions{
		SkipSafetyBackup: r.URL.Query().Get("safety_backup") == "false",
	})
	switch {
	case errors.Is(err, backups.ErrBackupNotFound):
		writeError(w, http.StatusNotFound, "backup not found")
	case err != nil && strings.HasPrefix(err.Error(), "can only restore"):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeLifecycleError(w, r, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "restoring", "operation": op})
	}
}

func (h *BackupHandlers) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	err = h.svc.Delete(r.Context(), srv, chi.URLParam(r, "backupId"), h.actor(r))
	switch {
	case errors.Is(err, backups.ErrBackupNotFound):
		writeError(w, http.StatusNotFound, "backup not found")
	case err != nil:
		if busy, ok := store.AsServerBusy(err); ok && busy.Current == nil {
			writeCodedError(w, http.StatusConflict, "server_busy", "cannot delete a running backup")
			return
		}
		writeLifecycleError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// DownloadBackup streams a backup archive. It is a plain navigation, so it
// accepts a ticket bound to this exact path.
func (h *BackupHandlers) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	serverID, backupID := chi.URLParam(r, "id"), chi.URLParam(r, "backupId")
	b, err := h.store.GetBackup(r.Context(), backupID)
	if err != nil || b.ServerID != serverID || b.Status != "success" {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	srv, c, ok := serverAgent(w, r, h.store, serverID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Hour)
	defer cancel()
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeCodedError(w, http.StatusBadGateway, "node_unreachable", "the node could not be reached")
		return
	}
	proxyReq := r.Clone(ctx)
	proxyReq.URL.RawQuery = ""
	audit(h.store, r, serverID, "backup.download", map[string]any{"backup_id": backupID})
	c.ProxyHTTP(ctx, w, proxyReq, "/agent/v1/servers/"+srv.ID+"/backups/"+backupID+"/download")
}

func (h *BackupHandlers) ListTargets(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	targets, err := h.store.ListBackupTargets(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "list backup targets", err)
		return
	}
	if targets == nil {
		targets = []*store.BackupTarget{}
	}
	writeJSON(w, http.StatusOK, targets)
}

// backupTargetBody is a backup destination. Only "local" (the node's own
// backup folder) exists today; retention is what a target configures.
type backupTargetBody struct {
	Name      *string         `json:"name"`
	Type      *string         `json:"type"`
	Config    json.RawMessage `json:"config"`
	Retention json.RawMessage `json:"retention"`
	IsDefault *bool           `json:"is_default"`
}

func validateTarget(t *store.BackupTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 60 {
		return badField("name", "name is required and must be at most 60 characters")
	}
	if t.Type != "local" {
		return badField("type", "the only supported backup target type is local")
	}
	if len(t.Config) > 0 && string(t.Config) != "{}" && string(t.Config) != "null" {
		return badField("config", "local targets take no configuration")
	}
	t.Config = json.RawMessage("{}")
	if len(t.Retention) > 0 && string(t.Retention) != "null" {
		var p backups.Policy
		dec := json.NewDecoder(strings.NewReader(string(t.Retention)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return badField("retention", "retention takes keep_last_n and max_age_days")
		}
		if p.KeepLastN < 0 || p.KeepLastN > 1000 || p.MaxAgeDays < 0 || p.MaxAgeDays > 3650 {
			return badField("retention", "keep_last_n must be 0-1000 and max_age_days 0-3650")
		}
	}
	return nil
}

func (h *BackupHandlers) CreateTarget(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	var body backupTargetBody
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t := &store.BackupTarget{ServerID: serverID, Type: "local", Config: body.Config, Retention: body.Retention}
	if body.Name != nil {
		t.Name = *body.Name
	}
	if body.Type != nil {
		t.Type = *body.Type
	}
	if body.IsDefault != nil {
		t.IsDefault = *body.IsDefault
	}
	if err := validateTarget(t); err != nil {
		respondInvalid(w, err)
		return
	}
	created, err := h.store.CreateBackupTarget(r.Context(), t)
	if err != nil {
		writeServerError(w, r, "create backup target", err)
		return
	}
	audit(h.store, r, serverID, "backup_target.create", map[string]any{"target_id": created.ID, "retention": json.RawMessage(created.Retention)})
	writeJSON(w, http.StatusCreated, created)
}

func (h *BackupHandlers) UpdateTarget(w http.ResponseWriter, r *http.Request) {
	serverID, targetID := chi.URLParam(r, "id"), chi.URLParam(r, "targetId")
	t, err := h.store.GetBackupTarget(r.Context(), targetID)
	if err != nil || t.ServerID != serverID {
		writeError(w, http.StatusNotFound, "backup target not found")
		return
	}
	var body backupTargetBody
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name != nil {
		t.Name = *body.Name
	}
	if body.Type != nil {
		t.Type = *body.Type
	}
	if body.Config != nil {
		t.Config = body.Config
	}
	if body.Retention != nil {
		t.Retention = body.Retention
	}
	if body.IsDefault != nil {
		t.IsDefault = *body.IsDefault
	}
	if err := validateTarget(t); err != nil {
		respondInvalid(w, err)
		return
	}
	if err := h.store.UpdateBackupTarget(r.Context(), t); err != nil {
		writeServerError(w, r, "update backup target", err)
		return
	}
	audit(h.store, r, serverID, "backup_target.update", map[string]any{"target_id": t.ID, "retention": json.RawMessage(t.Retention)})
	writeJSON(w, http.StatusOK, t)
}

func (h *BackupHandlers) DeleteTarget(w http.ResponseWriter, r *http.Request) {
	serverID, targetID := chi.URLParam(r, "id"), chi.URLParam(r, "targetId")
	t, err := h.store.GetBackupTarget(r.Context(), targetID)
	if err != nil || t.ServerID != serverID {
		writeError(w, http.StatusNotFound, "backup target not found")
		return
	}
	if err := h.store.DeleteBackupTarget(r.Context(), targetID); err != nil {
		writeServerError(w, r, "delete backup target", err)
		return
	}
	audit(h.store, r, serverID, "backup_target.delete", map[string]any{"target_id": targetID})
	w.WriteHeader(http.StatusNoContent)
}
