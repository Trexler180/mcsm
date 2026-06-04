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
	"github.com/mcsm/api/internal/store"
)

const (
	pollInterval  = 15 * time.Second
	perCallBudget = 3 * time.Second
)

// Run blocks until ctx is done. Spawn it in its own goroutine.
func Run(ctx context.Context, s *store.Store) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()

	// First sweep immediately so the UI is correct on boot.
	pollAll(ctx, s)
	for {
		select {
		case <-t.C:
			pollAll(ctx, s)
		case <-ctx.Done():
			return
		}
	}
}

func pollAll(ctx context.Context, s *store.Store) {
	pollNodes(ctx, s)

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
			desired = v
		}

		if desired != "" && desired != srv.Status {
			if err := s.UpdateServerStatus(ctx, srv.ID, desired); err != nil {
				log.Printf("poller: update status %s -> %s: %v", srv.ID, desired, err)
			}
		}
	}
}

func pollNodes(ctx context.Context, s *store.Store) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, node := range nodes {
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
		callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
		info, err := c.Info(callCtx)
		cancel()
		if err != nil {
			continue
		}
		memoryMb := intFromInfo(info, "memory_mb")
		diskGb := intFromInfo(info, "disk_gb")
		cpuCores := intFromInfo(info, "cpu_cores")
		if err := s.UpdateNodeHeartbeat(ctx, node.ID, memoryMb, diskGb, cpuCores); err != nil {
			log.Printf("poller: update node heartbeat %s: %v", node.ID, err)
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
