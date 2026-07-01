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

	// First sweep immediately so the UI is correct on boot.
	pollAll(ctx, s, engine)
	for {
		select {
		case <-t.C:
			pollAll(ctx, s, engine)
		case <-ctx.Done():
			return
		}
	}
}

func pollAll(ctx context.Context, s *store.Store, engine *notify.Engine) {
	pollNodes(ctx, s, engine)

	servers, err := s.ListServers(ctx)
	if err != nil {
		return
	}
	// Cache nodes to avoid hitting the DB once per server in the common
	// "few nodes, many servers" case.
	nodes := map[string]*store.Node{}
	for _, srv := range servers {
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
			if srv.Status == "online" && desired == "offline" {
				recent, _ := s.HasRecentLifecycleAction(ctx, srv.ID, crashGrace)
				if !recent {
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
			if err := s.UpdateServerStatus(ctx, srv.ID, desired); err != nil {
				log.Printf("poller: update status %s -> %s: %v", srv.ID, desired, err)
			}
		}
	}
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
		memoryMb := intFromInfo(info, "memory_mb")
		diskGb := intFromInfo(info, "disk_gb")
		cpuCores := intFromInfo(info, "cpu_cores")
		if err := s.UpdateNodeHeartbeat(ctx, node.ID, memoryMb, diskGb, cpuCores); err != nil {
			log.Printf("poller: update node heartbeat %s: %v", node.ID, err)
		}
		if !wasOnline {
			engine.Emit(notify.NodeOnline(node.ID, node.Name))
		}
	}
}

func intFromInfo(info map[string]any, key string) *int {
	v, ok := info[key]
	if !ok {
		return nil
	}
	var out int
	switch n := v.(type) {
	case float64:
		out = int(n)
	case int:
		out = n
	case int64:
		out = int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return nil
		}
		out = int(i)
	default:
		return nil
	}
	return &out
}
