package store

import (
	"context"
	"time"
)

// ── Retention ────────────────────────────────────────────────────
//
// Two of the MCP tables are written by endpoints that are open to the internet
// by necessity: a client must be able to register, and start an authorization,
// before anyone has authenticated. Rate limits bound how fast those rows
// arrive; nothing bounded how many of them accumulated, so the table only ever
// grew — including for deployments nobody ever connected an agent to.
//
// Nothing here is load-bearing for authorization. Every check that matters
// already re-reads expiry live, so a row that outlives its purpose is inert
// long before it is deleted. This is housekeeping, and it is written to be
// obviously safe rather than thorough: it only ever removes rows that can no
// longer authorize anything, and never a client that still has a grant.

const (
	// mcpSpentRetention is how long a spent artifact stays readable after it
	// stops being usable. Short, but not instant: an operator investigating a
	// failed connection an hour later should still find its traces.
	mcpSpentRetention = 48 * time.Hour

	// mcpClientRetention is how long a registered client with nothing attached
	// to it survives. A client that never reached consent is a probe, an
	// abandoned attempt, or a retry that registered twice.
	mcpClientRetention = 7 * 24 * time.Hour

	// mcpActionRetention keeps settled approval requests around long enough to
	// be reviewed alongside the audit entries that reference them.
	mcpActionRetention = 30 * 24 * time.Hour
)

// MCPRetentionReport counts what one sweep removed, so the caller can log
// something more useful than "done".
type MCPRetentionReport struct {
	Requests int64
	Tokens   int64
	Actions  int64
	Clients  int64
}

// Total reports whether the sweep did anything at all.
func (r MCPRetentionReport) Total() int64 {
	return r.Requests + r.Tokens + r.Actions + r.Clients
}

// PurgeExpiredMCPRecords deletes MCP rows that can no longer authorize
// anything. It is safe to call at any time and on any schedule.
//
// The order matters only for tidiness: authorization requests cascade to the
// codes issued against them, and grants cascade to their tokens, so removing
// the parents first leaves less for the later statements to find.
func (s *Store) PurgeExpiredMCPRecords(ctx context.Context) (MCPRetentionReport, error) {
	var report MCPRetentionReport
	now := time.Now().UTC()

	// Parked /authorize calls, once they are past their deadline. The codes
	// minted against them cascade. A request that became a grant is not
	// referenced by that grant, so this never touches a live delegation.
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mcp_authorization_requests WHERE expires_at < ?`, now.Add(-mcpSpentRetention))
	if err != nil {
		return report, err
	}
	report.Requests, _ = res.RowsAffected()

	// Access and refresh tokens that are expired or revoked. A revoked token is
	// kept for the retention window so "this stopped working when?" stays
	// answerable, then goes.
	res, err = s.db.ExecContext(ctx,
		`DELETE FROM mcp_tokens
		  WHERE (expires_at < ? ) OR (revoked_at IS NOT NULL AND revoked_at < ?)`,
		now.Add(-mcpSpentRetention), now.Add(-mcpSpentRetention))
	if err != nil {
		return report, err
	}
	report.Tokens, _ = res.RowsAffected()

	// Settled approval requests. Pending ones are never removed here — they are
	// settled by ExpireStaleMCPActionRequests first, on their own deadline.
	res, err = s.db.ExecContext(ctx,
		`DELETE FROM mcp_action_requests WHERE status != ? AND created_at < ?`,
		MCPActionPending, now.Add(-mcpActionRetention))
	if err != nil {
		return report, err
	}
	report.Actions, _ = res.RowsAffected()

	// Registered clients with nothing attached. The NOT EXISTS pair is the
	// whole safety argument: a client that any grant or any live authorization
	// request still names is left alone, because deleting it would cascade
	// straight through to a working delegation.
	res, err = s.db.ExecContext(ctx, `
		DELETE FROM mcp_clients
		 WHERE created_at < ?
		   AND NOT EXISTS (SELECT 1 FROM mcp_grants g WHERE g.client_id = mcp_clients.client_id)
		   AND NOT EXISTS (SELECT 1 FROM mcp_authorization_requests r WHERE r.client_id = mcp_clients.client_id)`,
		now.Add(-mcpClientRetention))
	if err != nil {
		return report, err
	}
	report.Clients, _ = res.RowsAffected()

	// Spent and lapsed backup confirmations. These are short-lived by design;
	// there is nothing to learn from an old one.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mcp_backup_confirmations WHERE expires_at < ?`, now.Add(-mcpSpentRetention)); err != nil {
		return report, err
	}

	return report, nil
}
