// Package poller keeps the panel's view of each server's status fresh by
// asking the agent for it on a fixed interval. Without this, a server that
// crashes outside of a panel-initiated stop would still show as "online".
package poller

import (
	"context"
	"log"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

const (
	pollInterval = 15 * time.Second
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
