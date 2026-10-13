package handlers

import (
	"net/http"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// Fleet is the Home screen in one request: the headline figures, a row per
// server with its live numbers and a 24-hour player series, node health, the
// next day of schedules, and recent activity. It reads only what the panel
// already stores — samples the poller took, rows the scheduler keeps, the last
// mod-update check — never a node or an upstream API, so it stays cheap to
// poll and fast to render. Live changes then arrive on the event stream.

// sampleFreshness is how old a server's latest sample may be and still count
// as its current numbers.
const sampleFreshness = 2 * time.Minute

// backupStaleAfter is how long a server may go without a successful backup
// before the summary counts it as overdue.
const backupStaleAfter = 24 * time.Hour

type fleetServer struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Status         string           `json:"status"`
	Platform       string           `json:"platform"`
	MCVersion      string           `json:"mc_version"`
	NodeID         string           `json:"node_id"`
	FolderID       *string          `json:"folder_id"`
	SortOrder      int              `json:"sort_order"`
	Port           int              `json:"port"`
	OnlineSince    *int64           `json:"online_since"`
	Players        *int             `json:"players"`
	TPS            *float64         `json:"tps"`
	CPUPercent     *float64         `json:"cpu_percent"`
	RAMUsedMB      *int64           `json:"ram_used_mb"`
	RAMLimitMB     int              `json:"ram_limit_mb"`
	SampledAt      *time.Time       `json:"sampled_at"`
	Players24h     []*int           `json:"players_24h"`
	LastBackupAt   *time.Time       `json:"last_backup_at"`
	BackupOverdue  bool             `json:"backup_overdue"`
	ActiveConflict bool             `json:"active_conflict"`
	ModUpdates     *int             `json:"mod_updates"`
	Operation      *store.Operation `json:"operation"`
}

type fleetNode struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Online     bool       `json:"online"`
	CPUPct     *float64   `json:"cpu_pct"`
	MemUsedMb  *int       `json:"mem_used_mb"`
	MemoryMb   *int       `json:"memory_mb"`
	DiskUsedGb *int       `json:"disk_used_gb"`
	DiskGb     *int       `json:"disk_gb"`
	LastSeen   *time.Time `json:"last_seen"`
}

type fleetUpcoming struct {
	store.UpcomingTask
	ServerName string `json:"server_name"`
}

type fleetFigures struct {
	PlayersOnline  int `json:"players_online"`
	ServersUp      int `json:"servers_up"`
	ServersTotal   int `json:"servers_total"`
	ServersUnknown int `json:"servers_unknown"`
	NodesOnline    int `json:"nodes_online"`
	NodesTotal     int `json:"nodes_total"`
	AlertsUnread   int `json:"alerts_unread"`
	BackupsOverdue int `json:"backups_overdue"`
	ModUpdates     int `json:"mod_updates"`
}

type fleetResponse struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Figures     fleetFigures        `json:"figures"`
	Servers     []fleetServer       `json:"servers"`
	Nodes       []fleetNode         `json:"nodes"`
	Upcoming    []fleetUpcoming     `json:"upcoming"`
	Activity    []*store.AuditEntry `json:"activity"`
	// EventSeq is the event stream position this snapshot reflects; open the
	// stream with ?since=<event_seq> to receive only what changed after it.
	EventSeq int64 `json:"event_seq"`
}

// Fleet answers GET /api/v1/fleet.
func (h *OverviewHandlers) Fleet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := auth.ClaimsFrom(ctx)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	isAdmin, err := callerIsAdmin(r, h.store)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	now := time.Now().UTC()
	out := fleetResponse{
		GeneratedAt: now,
		Servers:     []fleetServer{},
		Nodes:       []fleetNode{},
		Upcoming:    []fleetUpcoming{},
		Activity:    []*store.AuditEntry{},
	}
	if h.bus != nil {
		out.EventSeq = h.bus.Seq()
	}

	var servers []*store.Server
	if isAdmin {
		servers, err = h.store.ListServers(ctx)
	} else {
		servers, err = h.store.ListServersForUser(ctx, claims.UserID)
	}
	if err != nil {
		writeServerError(w, r, "fleet: list servers", err)
		return
	}
	if machine := auth.MachineFrom(ctx); machine != nil {
		kept := servers[:0]
		for _, srv := range servers {
			if machine.AllowsServer(srv.ID) {
				kept = append(kept, srv)
			}
		}
		servers = kept
	}

	// Everything below is one query each for the whole fleet.
	samples, err := h.store.LatestServerSamples(ctx, now.Add(-sampleFreshness))
	if err != nil {
		writeServerError(w, r, "fleet: samples", err)
		return
	}
	dayStart := now.Truncate(time.Hour).Add(-23 * time.Hour)
	peaks, err := h.store.HourlyPlayerPeaks(ctx, dayStart)
	if err != nil {
		writeServerError(w, r, "fleet: player series", err)
		return
	}
	backups, _ := h.store.LatestSuccessfulBackups(ctx)
	checks, _ := h.store.ModUpdateChecks(ctx)
	conflicts := map[string]bool{}
	if list, err := h.store.ListActiveConflicts(ctx); err == nil {
		for _, c := range list {
			conflicts[c.ServerID] = true
		}
	}
	running := map[string]*store.Operation{}
	if ops, err := h.store.ListRunningOperations(ctx); err == nil {
		for _, op := range ops {
			if op.Exclusive || running[op.ServerID] == nil {
				running[op.ServerID] = op
			}
		}
	}

	visible := map[string]*store.Server{}
	nodeIDs := map[string]bool{}
	serverIDs := make([]string, 0, len(servers))
	for _, srv := range servers {
		visible[srv.ID] = srv
		nodeIDs[srv.NodeID] = true
		serverIDs = append(serverIDs, srv.ID)

		fs := fleetServer{
			ID: srv.ID, Name: srv.Name, Status: srv.Status, Platform: srv.Platform, MCVersion: srv.MCVersion,
			NodeID: srv.NodeID, FolderID: srv.FolderID, SortOrder: srv.SortOrder, Port: srv.Port,
			OnlineSince: srv.OnlineSince, RAMLimitMB: srv.RAMMbMax,
			ActiveConflict: conflicts[srv.ID], Operation: running[srv.ID],
			Players24h: make([]*int, 24),
		}
		if smp, ok := samples[srv.ID]; ok && srv.Status == "online" {
			players, cpu, ram, at := smp.Players, smp.CPUPercent, smp.RAMUsedMB, smp.TS
			fs.Players, fs.CPUPercent, fs.RAMUsedMB, fs.SampledAt, fs.TPS = &players, &cpu, &ram, &at, smp.TPS
			out.Figures.PlayersOnline += players
		}
		for i := range 24 {
			if peak, ok := peaks[srv.ID][dayStart.Add(time.Duration(i)*time.Hour).Unix()]; ok {
				fs.Players24h[i] = &peak
			}
		}
		if at, ok := backups[srv.ID]; ok {
			fs.LastBackupAt = &at
		}
		fs.BackupOverdue = fs.LastBackupAt == nil || now.Sub(*fs.LastBackupAt) > backupStaleAfter
		if fs.BackupOverdue {
			out.Figures.BackupsOverdue++
		}
		if c, ok := checks[srv.ID]; ok {
			n := c.Available
			fs.ModUpdates = &n
			out.Figures.ModUpdates += n
		}
		switch srv.Status {
		case "online":
			out.Figures.ServersUp++
		case "unknown":
			out.Figures.ServersUnknown++
		}
		out.Servers = append(out.Servers, fs)
	}
	out.Figures.ServersTotal = len(servers)

	if nodes, err := h.store.ListNodes(ctx); err == nil {
		for _, n := range nodes {
			if !isAdmin && !nodeIDs[n.ID] {
				continue
			}
			online := n.LastSeen != nil && now.Sub(*n.LastSeen) <= nodeFreshWindow
			if online {
				out.Figures.NodesOnline++
			}
			out.Figures.NodesTotal++
			out.Nodes = append(out.Nodes, fleetNode{
				ID: n.ID, Name: n.Name, Online: online, CPUPct: n.CPUPct,
				MemUsedMb: n.MemUsedMb, MemoryMb: n.MemoryMb,
				DiskUsedGb: n.DiskUsedGb, DiskGb: n.DiskGb, LastSeen: n.LastSeen,
			})
		}
	}

	if tasks, err := h.store.UpcomingTasks(ctx, now.Add(24*time.Hour)); err == nil {
		for _, t := range tasks {
			if srv, ok := visible[t.ServerID]; ok {
				out.Upcoming = append(out.Upcoming, fleetUpcoming{UpcomingTask: t, ServerName: srv.Name})
			}
		}
	}

	if n, err := h.store.UnreadCount(ctx, claims.UserID); err == nil {
		out.Figures.AlertsUnread = n
	}

	var scope *store.AuditScope
	if !isAdmin {
		scope = &store.AuditScope{ServerIDs: serverIDs, UserID: claims.UserID}
	}
	if entries, _, err := h.store.ListAuditPage(ctx, store.AuditQuery{Scope: scope, Limit: 15}); err == nil {
		out.Activity = entries
	}

	writeJSON(w, http.StatusOK, out)
}
