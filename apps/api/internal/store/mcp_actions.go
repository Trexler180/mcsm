package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ── MCP action approval queue ────────────────────────────────────
//
// An agent cannot start, stop, or restart anything. It can only file a request
// and watch it. A human approves the request on a ServerManager surface, with
// the same password/TOTP step-up used to issue credentials, and only then does
// the panel act.
//
// The reason the approval lives here rather than in the MCP host's own "allow
// this tool?" prompt is that the host prompt is inside software ServerManager
// does not control, does not version, and cannot audit. It is a fine extra
// layer; it is not the layer that decides whether a Minecraft server restarts.
//
// The status column is the concurrency control. Every transition out of
// `pending` is a conditional UPDATE, so an action executes at most once however
// many times it is approved, retried, or polled.

const (
	MCPActionPending   = "pending"
	MCPActionDenied    = "denied"
	MCPActionExpired   = "expired"
	MCPActionExecuting = "executing"
	MCPActionExecuted  = "executed"
	MCPActionFailed    = "failed"

	// MCPActionRequestLifetime bounds how long a request waits for a human.
	// Short on purpose: an approval should be a decision about the situation
	// the agent just described, not a stale button someone finds tomorrow.
	MCPActionRequestLifetime = 15 * time.Minute

	// mcpActionReasonMax bounds the model-authored justification. It is shown
	// to a human as untrusted text, so it must not be able to fill a screen.
	mcpActionReasonMax = 500
)

var (
	ErrMCPActionNotFound     = errors.New("action request not found")
	ErrMCPActionNotPending   = errors.New("action request is no longer pending")
	ErrMCPActionUnsupported  = errors.New("unsupported action request")
	ErrMCPActionServerDenied = errors.New("this grant does not cover that server")
)

// MCPActionRequest is one pending (or settled) request for a lifecycle action.
type MCPActionRequest struct {
	ID       string `json:"id"`
	GrantID  string `json:"grant_id"`
	UserID   string `json:"user_id"`
	ServerID string `json:"server_id"`
	Action   string `json:"action"`
	// Reason is model-authored text. It is displayed to the approver as
	// untrusted evidence and is never interpreted by the panel.
	Reason        string     `json:"reason"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	DecidedAt     *time.Time `json:"decided_at"`
	DecidedBy     *string    `json:"decided_by"`
	ExecutedAt    *time.Time `json:"executed_at"`
	FailureReason *string    `json:"failure_reason"`

	// ClientName and ServerName are joined for display; they are not columns.
	ClientName string `json:"client_name,omitempty"`
	ServerName string `json:"server_name,omitempty"`
}

// EffectiveStatus reports the status a caller should see. A request whose
// deadline has passed reads as expired even before anything sweeps it, so an
// agent polling a forgotten request is told the truth immediately.
func (a *MCPActionRequest) EffectiveStatus(now time.Time) string {
	if a == nil {
		return MCPActionExpired
	}
	if a.Status == MCPActionPending && !a.ExpiresAt.After(now) {
		return MCPActionExpired
	}
	return a.Status
}

const mcpActionColumns = `r.id, r.grant_id, r.user_id, r.server_id, r.action, r.reason, r.status,
	 r.created_at, r.expires_at, r.decided_at, r.decided_by, r.executed_at, r.failure_reason`

func scanMCPAction(scan func(dest ...any) error, extra ...any) (*MCPActionRequest, error) {
	var a MCPActionRequest
	dest := []any{&a.ID, &a.GrantID, &a.UserID, &a.ServerID, &a.Action, &a.Reason, &a.Status,
		&a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecidedBy, &a.ExecutedAt, &a.FailureReason}
	dest = append(dest, extra...)
	if err := scan(dest...); err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateMCPActionRequest files a request. It performs no action and touches no
// node: it exists so a human can decide.
//
// The grant/server/scope check belongs to the caller, which holds the live
// principal; this method owns shape, the supported-action list, and the
// deadline.
func (s *Store) CreateMCPActionRequest(ctx context.Context, grantID, userID, serverID, action, reason string) (*MCPActionRequest, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if _, ok := MCPActionPermission(action); !ok {
		return nil, ErrMCPActionUnsupported
	}
	reason = truncateRunes(strings.TrimSpace(reason), mcpActionReasonMax)
	now := time.Now().UTC()
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_action_requests (id, grant_id, user_id, server_id, action, reason, status, created_at, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		id, grantID, userID, serverID, action, reason, MCPActionPending, now, now.Add(MCPActionRequestLifetime),
	); err != nil {
		return nil, err
	}
	return s.GetMCPActionRequest(ctx, id)
}

func (s *Store) GetMCPActionRequest(ctx context.Context, id string) (*MCPActionRequest, error) {
	a, err := scanMCPAction(s.db.QueryRowContext(ctx,
		`SELECT `+mcpActionColumns+` FROM mcp_action_requests r WHERE r.id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPActionNotFound
	}
	return a, err
}

// GetMCPActionRequestForGrant reads a request scoped to the grant that filed
// it, so one agent can never observe another delegation's requests.
func (s *Store) GetMCPActionRequestForGrant(ctx context.Context, id, grantID string) (*MCPActionRequest, error) {
	a, err := scanMCPAction(s.db.QueryRowContext(ctx,
		`SELECT `+mcpActionColumns+` FROM mcp_action_requests r WHERE r.id = ? AND r.grant_id = ?`, id, grantID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPActionNotFound
	}
	return a, err
}

// ListMCPActionRequestsForUser returns the owner's recent requests, newest
// first, joined with the client and server names the approval screen shows.
func (s *Store) ListMCPActionRequestsForUser(ctx context.Context, userID string, limit int) ([]*MCPActionRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mcpActionColumns+`, g.client_name, COALESCE(sv.name, '')
		   FROM mcp_action_requests r
		   JOIN mcp_grants g ON g.id = r.grant_id
		   LEFT JOIN servers sv ON sv.id = r.server_id
		  WHERE r.user_id = ?
		  ORDER BY r.created_at DESC, r.id
		  LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MCPActionRequest{}
	for rows.Next() {
		var clientName, serverName string
		a, err := scanMCPAction(rows.Scan, &clientName, &serverName)
		if err != nil {
			return nil, err
		}
		a.ClientName = clientName
		a.ServerName = serverName
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClaimMCPActionRequest is the at-most-once guarantee. It moves a request from
// pending to executing with a conditional UPDATE; only the caller that sees one
// affected row may go on to touch a node. A repeated approval, a concurrent
// approval, and a retried request all lose harmlessly.
//
// The deadline is checked in Go against the row we just read rather than in
// SQL, so the transition does not depend on the driver's datetime comparison
// semantics.
func (s *Store) ClaimMCPActionRequest(ctx context.Context, id, userID, deciderID string) (*MCPActionRequest, error) {
	req, err := s.GetMCPActionRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.UserID != userID {
		// Another owner's request is reported exactly like one that does not
		// exist.
		return nil, ErrMCPActionNotFound
	}
	now := time.Now().UTC()
	if req.EffectiveStatus(now) != MCPActionPending {
		return nil, ErrMCPActionNotPending
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE mcp_action_requests SET status = ?, decided_at = ?, decided_by = ?
		  WHERE id = ? AND status = ?`,
		MCPActionExecuting, now, nullableDecider(deciderID), id, MCPActionPending)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrMCPActionNotPending
	}
	return s.GetMCPActionRequest(ctx, id)
}

// nullableDecider maps "no human decided this" onto SQL NULL. An auto-approved
// request has no decider, and recording the owner's id would be a lie the audit
// log could never be talked out of: it would read as though someone looked at
// the request and pressed approve. decided_by is a foreign key to users, so ""
// is not a storable alternative.
func nullableDecider(deciderID string) any {
	if deciderID == "" {
		return nil
	}
	return deciderID
}

// DenyMCPActionRequest settles a request without acting. Also conditional, so a
// deny racing an approve resolves to exactly one outcome.
func (s *Store) DenyMCPActionRequest(ctx context.Context, id, userID, deciderID string) error {
	req, err := s.GetMCPActionRequest(ctx, id)
	if err != nil {
		return err
	}
	if req.UserID != userID {
		return ErrMCPActionNotFound
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE mcp_action_requests SET status = ?, decided_at = ?, decided_by = ?
		  WHERE id = ? AND status = ?`,
		MCPActionDenied, time.Now().UTC(), deciderID, id, MCPActionPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMCPActionNotPending
	}
	return nil
}

// FinishMCPActionRequest records the outcome of a claimed request. An empty
// failure means it succeeded.
func (s *Store) FinishMCPActionRequest(ctx context.Context, id, failure string) error {
	status := MCPActionExecuted
	if failure != "" {
		status = MCPActionFailed
		if len(failure) > mcpActionReasonMax {
			failure = failure[:mcpActionReasonMax]
		}
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE mcp_action_requests SET status = ?, executed_at = ?, failure_reason = ?
		  WHERE id = ? AND status = ?`,
		status, time.Now().UTC(), nullIfEmpty(failure), id, MCPActionExecuting)
	return err
}

// ExpireStaleMCPActionRequests settles requests nobody decided in time.
//
// Called from two places: opportunistically when the owner's queue is read, so
// the queue a human sees never contains a button that would fail if pressed,
// and from the retention sweep, so a request on a queue nobody opens still
// settles rather than sitting `pending` forever.
//
// Reads are correct without it either way — EffectiveStatus compares the
// deadline on the way out — but the stored status should not disagree with what
// every caller is told.
func (s *Store) ExpireStaleMCPActionRequests(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, expires_at FROM mcp_action_requests WHERE status = ?`, MCPActionPending)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now()
	var stale []string
	for rows.Next() {
		var id string
		var expiresAt time.Time
		if err := rows.Scan(&id, &expiresAt); err != nil {
			return err
		}
		if !expiresAt.After(now) {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE mcp_action_requests SET status = ? WHERE id = ? AND status = ?`,
			MCPActionExpired, id, MCPActionPending); err != nil {
			return err
		}
	}
	return nil
}
