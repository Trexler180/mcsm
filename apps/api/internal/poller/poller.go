// Package poller keeps the panel's view of each server's status fresh by
// asking the agent for it on a fixed interval. Without this, a server that
// crashes outside of a panel-initiated stop would still show as "online".
package poller

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

const (
	pollInterval  = 15 * time.Second
	perCallBudget = 3 * time.Second
	// A restart issues no DB status change and the process is briefly offline
	// between stop and start; tolerate that window before calling an offline a
	// crash. Also covers a stop/kill whose audit row lands just after the poll.
	crashGrace = 2 * time.Minute
	// nodeFreshWindow mirrors the overview's notion of a live node: not heard
	// from within this window means offline. Used to detect up/down transitions.
	nodeFreshWindow = 45 * time.Second
)

// Run blocks until ctx is done. Spawn it in its own goroutine. engine may be nil
// (notifications disabled), in which case Emit is a no-op.
func Run(ctx context.Context, s *store.Store, engine *notify.Engine) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()

	sm := newSampler()
	sessions := newSessionTracker(s)
	denials := newDenialTracker()
	tick := 0
	sweep := func() {
		pollAll(ctx, s, engine, denials)
		// Sample resource history on a slower cadence than status polling; each
		// snapshot's roster also drives player-session tracking.
		if tick%sampleEvery == 0 {
			sm.sampleAll(ctx, s, engine, func(srv *store.Server, stats *agent.ServerStats) {
				sessions.observe(ctx, srv, stats)
			})
		}
		tick++
	}

	// First sweep immediately so the UI is correct on boot.
	sweep()
	for {
		select {
		case <-t.C:
			sweep()
		case <-ctx.Done():
			return
		}
	}
}

func pollAll(ctx context.Context, s *store.Store, engine *notify.Engine, denials *denialTracker) {
	pollNodes(ctx, s, engine)

	servers, err := s.ListServers(ctx)
	if err != nil {
		return
	}
	live := make(map[string]struct{}, len(servers))
	// Cache nodes to avoid hitting the DB once per server in the common
	// "few nodes, many servers" case.
	nodes := map[string]*store.Node{}
	for _, srv := range servers {
		live[srv.ID] = struct{}{}
		node, ok := nodes[srv.NodeID]
		if !ok {
			n, err := s.GetNode(ctx, srv.NodeID)
			if err != nil {
				continue
			}
			nodes[srv.NodeID] = n
			node = n
		}
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

		callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
		status, err := c.GetStatus(callCtx, srv.ID)
		cancel()

		// Players the whitelist turned away ride along on the status payload, so
		// this costs no extra request. Handled before the status-change branch
		// below, which returns early in several cases — an operator still needs to
		// hear about someone knocking on a server whose status did not move.
		if err == nil {
			denials.observe(srv, status, engine)
		}

		desired := ""
		if err != nil {
			// Agent unreachable — only flip to offline if we previously thought it was up.
			if srv.Status == "online" || srv.Status == "starting" {
				desired = "offline"
			}
		} else if v, ok := status["status"].(string); ok && v != "" {
			// During first start the API marks the DB row "starting" before the
			// agent runs installers such as Spigot BuildTools. The agent has no
			// process instance yet, so /status reports offline until install
			// finishes and the Java process begins. Keep the persisted starting
			// state; the start handler rolls it back on failure.
			if srv.Status == "starting" && v == "offline" {
				continue
			}
			desired = v
		}

		if desired != "" && desired != srv.Status {
			// A server we believed was online going offline on its own — with no
			// recent panel-initiated stop/restart/kill to explain it — is a crash.
			// Record it as a first-class signal for the overview before we
			// overwrite the status (after which the transition is no longer visible).
			crashed := false
			if srv.Status == "online" && desired == "offline" {
				recent, _ := s.HasRecentLifecycleAction(ctx, srv.ID, crashGrace)
				if !recent {
					crashed = true
					msg := "Server went offline unexpectedly (possible crash)"
					s.LogAction(ctx, "", srv.ID, "server.crash", "", map[string]any{"detail": msg})
					_ = s.InsertLogEvent(ctx, srv.ID, "error", msg, "poller")
					engine.Emit(notify.ServerCrash(srv.ID, srv.Name))
				} else {
					engine.Emit(notify.ServerOffline(srv.ID, srv.Name))
				}
			}
			// A clean transition into the online state.
			if desired == "online" && srv.Status != "online" {
				engine.Emit(notify.ServerOnline(srv.ID, srv.Name))
			}
			// Conversely, a server reaching "online" booted cleanly, so any stored
			// mod conflict is now resolved — whether the operator disabled the
			// offending jars, changed a mod version, or removed the jar by hand.
			// Mod conflicts block startup, so the server can't be online with one
			// live; the detail page goes quiet on its own (it reads the live agent
			// status), but the cockpit's cross-server feed would keep flagging a
			// now-healthy server until we clear the record here.
			if desired == "online" {
				if err := s.ResolveServerConflicts(ctx, srv.ID); err != nil {
					log.Printf("poller: resolve conflicts %s: %v", srv.ID, err)
				}
			}
			// The crash-attributed variant stamps the closed uptime segment's
			// end_reason so the stats page can tell crashes from clean stops.
			update := s.UpdateServerStatus
			if crashed {
				update = s.UpdateServerStatusCrash
			}
			if err := update(ctx, srv.ID, desired); err != nil {
				log.Printf("poller: update status %s -> %s: %v", srv.ID, desired, err)
			}
		}
	}

	// Servers deleted since the last sweep leave their ledger behind otherwise.
	denials.reap(live)
}

func pollNodes(ctx context.Context, s *store.Store, engine *notify.Engine) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, node := range nodes {
		// Prior liveness, derived from the last heartbeat, lets us alert only on
		// the up→down / down→up edges rather than every poll.
		wasOnline := node.LastSeen != nil && time.Since(*node.LastSeen) < nodeFreshWindow

		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
		info, err := c.Info(callCtx)
		cancel()
		if err != nil {
			if wasOnline {
				engine.Emit(notify.NodeOffline(node.ID, node.Name))
			}
			continue
		}
		hb := store.NodeHeartbeat{
			MemoryMb:      intFromInfo(info, "memory_mb"),
			DiskGb:        intFromInfo(info, "disk_gb"),
			CPUCores:      intFromInfo(info, "cpu_cores"),
			MemUsedMb:     intFromInfo(info, "mem_used_mb"),
			DiskUsedGb:    intFromInfo(info, "disk_used_gb"),
			CPUPct:        floatFromInfo(info, "cpu_pct"),
			UptimeSeconds: int64FromInfo(info, "uptime_seconds"),
			OS:            strFromInfo(info, "os"),
			Arch:          strFromInfo(info, "arch"),
			AgentVersion:  strFromInfo(info, "version"),
		}
		if err := s.UpdateNodeHeartbeat(ctx, node.ID, hb); err != nil {
			log.Printf("poller: update node heartbeat %s: %v", node.ID, err)
		}
		if !wasOnline {
			engine.Emit(notify.NodeOnline(node.ID, node.Name))
		}
	}
}

func intFromInfo(info map[string]any, key string) *int {
	i := int64FromInfo(info, key)
	if i == nil {
		return nil
	}
	out := int(*i)
	return &out
}

func int64FromInfo(info map[string]any, key string) *int64 {
	f := floatFromInfo(info, key)
	if f == nil {
		return nil
	}
	out := int64(*f)
	return &out
}

func floatFromInfo(info map[string]any, key string) *float64 {
	v, ok := info[key]
	if !ok {
		return nil
	}
	var out float64
	switch n := v.(type) {
	case float64:
		out = n
	case int:
		out = float64(n)
	case int64:
		out = float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		out = f
	default:
		return nil
	}
	return &out
}

func strFromInfo(info map[string]any, key string) *string {
	s, ok := info[key].(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}
