// Package mcpjanitor sweeps MCP records that can no longer authorize anything.
//
// It exists because two of the MCP endpoints are open to the internet by
// necessity — a client must be able to register, and to start an authorization,
// before anyone has authenticated — and both write a durable row. Rate limits
// bound the arrival rate; without this, nothing bounded the total.
//
// Nothing here is part of an authorization decision. Every check that matters
// re-reads expiry live, so these rows are inert long before they are deleted.
// A failed sweep is logged and retried on the next tick.
package mcpjanitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/mcsm/api/internal/store"
)

// interval is deliberately slow. Nothing depends on the sweep being timely, and
// a panel that is idle overnight should not be doing hourly deletes for it.
const interval = 6 * time.Hour

// startupDelay lets the API finish coming up before the first sweep. Boot is
// the busiest the database gets, and this is the least urgent thing on it.
const startupDelay = 5 * time.Minute

// Run sweeps on a fixed interval until ctx is cancelled.
func Run(ctx context.Context, s *store.Store) {
	timer := time.NewTimer(startupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		sweep(ctx, s)
		timer.Reset(interval)
	}
}

func sweep(ctx context.Context, s *store.Store) {
	// Settle lapsed approval requests first, so the retention pass below sees
	// them as the settled rows they are rather than skipping them as pending.
	if err := s.ExpireStaleMCPActionRequests(ctx); err != nil {
		slog.Warn("mcp janitor: expiring stale action requests failed", "error", err)
	}
	report, err := s.PurgeExpiredMCPRecords(ctx)
	if err != nil {
		slog.Warn("mcp janitor: purge failed", "error", err)
		return
	}
	if report.Total() == 0 {
		return
	}
	slog.Info("mcp janitor: swept expired records",
		"authorization_requests", report.Requests,
		"tokens", report.Tokens,
		"action_requests", report.Actions,
		"clients", report.Clients)
}
