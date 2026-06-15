package handlers

import (
	"net/http"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// OverviewHandlers powers the ops-cockpit dashboard with a single aggregate
// endpoint, so the home screen makes one request instead of fanning out per
// server. It deliberately uses only cheap DB-local signals (status, conflicts,
// crashes, backups, node health, recent activity) — never per-mod live source
// lookups — so it stays fast under the dashboard's short poll interval.
type OverviewHandlers struct {
	store *store.Store
}

func NewOverviewHandlers(s *store.Store) *OverviewHandlers {
	return &OverviewHandlers{store: s}
}

// nodeFreshWindow mirrors the poller's heartbeat cadence: a node not heard from
// within this window is treated as offline.
const nodeFreshWindow = 45 * time.Second

type serverOverview struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Platform       string     `json:"platform"`
	MCVersion      string     `json:"mc_version"`
	NodeID         string     `json:"node_id"`
	ActiveConflict bool       `json:"active_conflict"`
	LastBackupAt   *time.Time `json:"last_backup_at"`
	LastBackupOK   bool       `json:"last_backup_ok"`
}

type nodeOverview struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Online   bool       `json:"online"`
	MemoryMb *int       `json:"memory_mb"`
	LastSeen *time.Time `json:"last_seen"`
}

type overviewResponse struct {
	Servers   []serverOverview     `json:"servers"`
	Nodes     []nodeOverview       `json:"nodes"`
	Conflicts []*store.ModConflict `json:"conflicts"`
	Warnings  []*store.LogEvent    `json:"warnings"`
	Activity  []*store.AuditEntry  `json:"activity"`
	Counts    overviewCounts       `json:"counts"`
}

type overviewCounts struct {
	Servers       int `json:"servers"`
	Online        int `json:"online"`
	Transitioning int `json:"transitioning"`
	Conflicts     int `json:"conflicts"`
	OfflineNodes  int `json:"offline_nodes"`
}

// Overview assembles the cockpit payload. Non-admins only see their own servers;
// admins see everything. Node/activity/conflict feeds are scoped to that set.
func (h *OverviewHandlers) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := auth.ClaimsFrom(ctx)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var (
		servers []*store.Server
		err     error
	)
	if claims.Role == "admin" {
		servers, err = h.store.ListServers(ctx)
	} else {
		servers, err = h.store.ListServersForUser(ctx, claims.UserID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	allowed := map[string]bool{}
	for _, s := range servers {
		allowed[s.ID] = true
	}

	// Active conflicts, filtered to the caller's servers.
	allConflicts, _ := h.store.ListActiveConflicts(ctx)
	conflicts := []*store.ModConflict{}
	conflictByServer := map[string]bool{}
	for _, c := range allConflicts {
		if allowed[c.ServerID] {
			conflicts = append(conflicts, c)
			conflictByServer[c.ServerID] = true
		}
	}

	out := overviewResponse{
		Servers:   make([]serverOverview, 0, len(servers)),
		Nodes:     []nodeOverview{},
		Conflicts: conflicts,
		Warnings:  []*store.LogEvent{},
		Activity:  []*store.AuditEntry{},
	}
	out.Counts.Servers = len(servers)
	out.Counts.Conflicts = len(conflicts)

	nodeIDs := map[string]bool{}
	for _, s := range servers {
		switch s.Status {
		case "online":
			out.Counts.Online++
		case "starting", "stopping", "restarting":
			out.Counts.Transitioning++
		}
		nodeIDs[s.NodeID] = true

		so := serverOverview{
			ID:             s.ID,
			Name:           s.Name,
			Status:         s.Status,
			Platform:       s.Platform,
			MCVersion:      s.MCVersion,
			NodeID:         s.NodeID,
			ActiveConflict: conflictByServer[s.ID],
		}
		// Backup freshness: newest successful backup.
		if backups, err := h.store.ListBackups(ctx, s.ID); err == nil {
			for _, b := range backups {
				if b.Status == "success" && b.CompletedAt != nil {
					so.LastBackupAt = b.CompletedAt
					so.LastBackupOK = true
					break
				}
			}
		}
		out.Servers = append(out.Servers, so)
	}

	// Node health for nodes hosting the caller's servers.
	if nodes, err := h.store.ListNodes(ctx); err == nil {
		now := time.Now()
		for _, n := range nodes {
			if !nodeIDs[n.ID] {
				continue
			}
			online := n.LastSeen != nil && now.Sub(*n.LastSeen) <= nodeFreshWindow
			if !online {
				out.Counts.OfflineNodes++
			}
			out.Nodes = append(out.Nodes, nodeOverview{
				ID: n.ID, Name: n.Name, Online: online,
				MemoryMb: n.MemoryMb, LastSeen: n.LastSeen,
			})
		}
	}

	// Recent warnings/errors and audit activity. ListLogEvents/ListAudit with an
	// empty server id span all servers for admins; for non-admins we filter to
	// the allowed set below.
	if events, err := h.store.ListLogEvents(ctx, "", "", 25); err == nil {
		for _, e := range events {
			if allowed[e.ServerID] {
				out.Warnings = append(out.Warnings, e)
			}
		}
	}
	if entries, err := h.store.ListAudit(ctx, "", 50); err == nil {
		for _, e := range entries {
			if e.ServerID == nil || allowed[*e.ServerID] {
				out.Activity = append(out.Activity, e)
			}
			if len(out.Activity) >= 25 {
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}
