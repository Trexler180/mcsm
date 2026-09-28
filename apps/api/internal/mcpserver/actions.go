package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// ── Requesting an action ─────────────────────────────────────────
//
// The agent's half of the approval queue is deliberately thin: it can file a
// request and it can watch one. Neither call touches a node.
//
// The reason the decision is not simply delegated to the MCP host's own "allow
// this tool?" prompt is that the prompt lives in software ServerManager does
// not control, does not version, and cannot audit — and it answers "should this
// tool run?", not "should this Minecraft server restart right now?". The host
// prompt remains a welcome extra layer. It is not the layer that decides.

type requestActionInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
	Action   string `json:"action" jsonschema:"one of: start, stop, restart"`
	Reason   string `json:"reason" jsonschema:"a specific, honest justification the approving human will read; cite the evidence that led you here"`
}

type actionRequestOutput struct {
	RequestID string    `json:"request_id" jsonschema:"pass this to get_action_request to observe the outcome"`
	Status    string    `json:"status" jsonschema:"pending, denied, expired, executing, executed, or failed"`
	Server    ServerRef `json:"server" jsonschema:"trusted panel metadata for the target server"`
	Action    string    `json:"action" jsonschema:"the requested lifecycle action"`
	ExpiresAt string    `json:"expires_at" jsonschema:"RFC 3339 deadline; an undecided request expires"`
	// FailureReason is only ever set for a failure ServerManager itself
	// produced, never a value copied from a node response.
	FailureReason string `json:"failure_reason,omitempty" jsonschema:"why execution failed, when it did"`
	Note          string `json:"note" jsonschema:"how to read this result"`
}

const pendingNote = "Nothing has happened yet. A human must approve this in the ServerManager dashboard. " +
	"Call await_action_request with this request_id to wait for their decision instead of ending your turn — it returns " +
	"as soon as they decide, and if it times out still pending, call it again. Never file a second request; duplicates " +
	"make the queue harder to read and do not make approval more likely."

func (svc *Service) requestServerAction(p *Principal) mcp.ToolHandlerFor[requestActionInput, actionRequestOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in requestActionInput) (*mcp.CallToolResult, actionRequestOutput, error) {
		action := strings.ToLower(strings.TrimSpace(in.Action))
		permission, ok := store.MCPActionPermission(action)
		if !ok {
			// Naming the three supported actions is safe and useful: the tool
			// description already says them, and a model that guessed "kill"
			// should learn it is not on the menu rather than retry variations.
			return nil, actionRequestOutput{}, toolError("only start, stop, and restart can be requested")
		}

		// Two authorization passes on purpose. The first is the scope's own
		// requirement (group access on power); the second is the exact leaf for
		// the action asked for, so a grant that can request a restart cannot
		// file a stop.
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionPower, true)
		if err != nil {
			return nil, actionRequestOutput{}, err
		}
		if _, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeActionsRequest, permission, false); err != nil {
			return nil, actionRequestOutput{}, err
		}

		req, err := svc.store.CreateMCPActionRequest(ctx, p.GrantID, p.UserID, srv.ID, action, in.Reason)
		if err != nil {
			if errors.Is(err, store.ErrMCPActionUnsupported) {
				return nil, actionRequestOutput{}, toolError("only start, stop, and restart can be requested")
			}
			slog.Error("mcp action request create", "grant_id", p.GrantID, "server_id", srv.ID, "error", err)
			return nil, actionRequestOutput{}, toolError("the request could not be filed right now")
		}

		// The audit entry names all three actors: the human who delegated, the
		// grant that acted, and — in the detail — the client that holds it.
		svc.audit(ctx, p, srv.ID, "mcp.action.requested", map[string]any{
			"request_id": req.ID,
			"action":     action,
			"client":     cleanName(p.ClientName),
			// The model's own words, bounded and cleaned exactly as they will be
			// rendered to the approver.
			"reason": clean(req.Reason, maxEvidenceChars),
		})

		req = svc.autoApproveIfPolicyAllows(ctx, p, req)
		svc.notifyActionFiled(ctx, p, req, srv)
		return nil, actionOutput(req, srv, svc.now()), nil
	}
}

type getActionInput struct {
	RequestID string `json:"request_id" jsonschema:"the id returned by request_server_action"`
}

func (svc *Service) getActionRequest(p *Principal) mcp.ToolHandlerFor[getActionInput, actionRequestOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getActionInput) (*mcp.CallToolResult, actionRequestOutput, error) {
		// Scoped to the grant that filed it, so one delegation can never observe
		// another's queue — not even one owned by the same person.
		req, err := svc.store.GetMCPActionRequestForGrant(ctx, in.RequestID, p.GrantID)
		if err != nil {
			return nil, actionRequestOutput{}, toolError("no such action request")
		}
		// Re-authorize the read itself: a grant revoked or narrowed since the
		// request was filed must not keep observing it.
		srv, err := svc.authorize(ctx, p, req.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionPower, true)
		if err != nil {
			return nil, actionRequestOutput{}, err
		}
		return nil, actionOutput(req, srv, svc.now()), nil
	}
}

// awaitPollInterval is how often the wait tool re-reads the row. The decision
// arrives via an HTTP handler writing to the same SQLite file, so there is
// nothing to subscribe to; a short poll inside one call is what turns "the
// agent ends its turn and the human has to re-prompt it" into "the agent picks
// up within a couple of seconds of the button being pressed".
const awaitPollInterval = 2 * time.Second

// awaitBudget bounds one wait. Two ceilings force a value well under the
// request's own 15-minute lifetime: mcpRequestTimeout caps every tool call at
// three minutes, and the transport is stateless JSON with no SSE, so there is
// no push to hang on. Returning at 150s leaves margin under that cap and under
// the call timeouts MCP hosts apply themselves.
const awaitBudget = 150 * time.Second

const awaitTimedOutNote = "Still waiting — nobody has decided yet, and this wait hit its time limit rather than the request " +
	"lapsing. The request is untouched and still pending. Call await_action_request again with the same request_id to keep " +
	"waiting; do not file a second request."

// awaitBusyNote is returned when this connection already holds the maximum
// number of open waits. It says the same thing as a timeout on purpose: the
// request is untouched, and calling again is the correct response to both.
const awaitBusyNote = "Still waiting — this connection already has as many waits open as it may hold at once, so this call " +
	"returned immediately instead of joining them. The request is untouched and still pending. Wait for one of your other " +
	"calls to return, then call await_action_request again with the same request_id; do not file a second request."

type awaitActionInput struct {
	RequestID string `json:"request_id" jsonschema:"the id returned by request_server_action"`
}

// awaitActionRequest blocks until a request is decided, then returns it.
//
// The authorization story is identical to getActionRequest — the same
// grant-scoped read and the same re-authorization — because this is that tool
// with a wait attached, not a new kind of access. Both checks are repeated on
// every poll rather than taken once at entry, so a grant revoked while the
// agent is waiting stops the wait instead of being noticed only afterwards.
func (svc *Service) awaitActionRequest(p *Principal) mcp.ToolHandlerFor[awaitActionInput, actionRequestOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in awaitActionInput) (*mcp.CallToolResult, actionRequestOutput, error) {
		// Read and authorize once before claiming a slot, so a caller that is at
		// its limit still learns the request's current state — and so a request
		// it may not see never consumes one.
		req, err := svc.store.GetMCPActionRequestForGrant(ctx, in.RequestID, p.GrantID)
		if err != nil {
			return nil, actionRequestOutput{}, toolError("no such action request")
		}
		srv, err := svc.authorize(ctx, p, req.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionPower, true)
		if err != nil {
			return nil, actionRequestOutput{}, err
		}
		if req.EffectiveStatus(svc.now()) != store.MCPActionPending {
			return nil, actionOutput(req, srv, svc.now()), nil
		}
		if !svc.beginWait(p.GrantID) {
			out := actionOutput(req, srv, svc.now())
			out.Note = awaitBusyNote
			return nil, out, nil
		}
		defer svc.endWait(p.GrantID)

		deadline := svc.now().Add(awaitBudget)
		ticker := time.NewTicker(awaitPollInterval)
		defer ticker.Stop()

		for {
			req, err = svc.store.GetMCPActionRequestForGrant(ctx, in.RequestID, p.GrantID)
			if err != nil {
				return nil, actionRequestOutput{}, toolError("no such action request")
			}
			srv, err = svc.authorize(ctx, p, req.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionPower, true)
			if err != nil {
				return nil, actionRequestOutput{}, err
			}
			now := svc.now()
			// EffectiveStatus is the authority on expiry: nothing sweeps the
			// table, so a lapsed request still reads 'pending' in the status
			// column and only the deadline comparison catches it.
			if req.EffectiveStatus(now) != store.MCPActionPending {
				return nil, actionOutput(req, srv, now), nil
			}
			if !now.Before(deadline) {
				out := actionOutput(req, srv, now)
				out.Note = awaitTimedOutNote
				return nil, out, nil
			}
			select {
			case <-ctx.Done():
				// The host gave up on the call. Report the request as it stands
				// rather than as an error: it is still pending and still valid.
				out := actionOutput(req, srv, now)
				out.Note = awaitTimedOutNote
				return nil, out, nil
			case <-ticker.C:
			}
		}
	}
}

func actionOutput(req *store.MCPActionRequest, srv *store.Server, now time.Time) actionRequestOutput {
	status := req.EffectiveStatus(now)
	out := actionRequestOutput{
		RequestID: req.ID,
		Status:    status,
		Server:    serverRef(srv),
		Action:    req.Action,
		ExpiresAt: req.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if req.FailureReason != nil {
		out.FailureReason = clean(*req.FailureReason, maxEvidenceChars)
	}
	switch status {
	case store.MCPActionPending:
		out.Note = pendingNote
	case store.MCPActionDenied:
		out.Note = "A human declined this. Accept the decision: do not re-file the same request or look for another route to the same effect."
	case store.MCPActionExpired:
		out.Note = "Nobody decided in time and the request lapsed. Nothing happened. File a fresh request only if the problem is still current."
	case store.MCPActionExecuted:
		out.Note = "A human approved this and ServerManager carried it out exactly once. Verify the effect with the diagnostic tools."
	case store.MCPActionFailed:
		out.Note = "The action was approved but did not complete. Report the failure; do not retry automatically."
	default:
		out.Note = "The action was approved and is running. Poll again shortly."
	}
	return out
}

// notifyActionFiled raises the dashboard alert for a request that was just
// filed — either the prompt to decide it, or, if policy already ran it, the
// after-the-fact notice that it happened.
//
// The second case is the one that matters most: auto-approval is the setting
// that removes the human from the loop, so it must not also remove the human's
// visibility. An operator who turned the toggle on should still find out what
// their agent did, without having to go looking for it.
func (svc *Service) notifyActionFiled(ctx context.Context, p *Principal, req *store.MCPActionRequest, srv *store.Server) {
	if svc.notifier == nil {
		return
	}
	// Only the pending case belongs here. A request that settled on the way
	// through was auto-approved, and ExecuteApprovedAction has already raised
	// the resolution alert for it — announcing it twice would be worse than not
	// announcing it at all.
	if req.EffectiveStatus(svc.now()) != store.MCPActionPending {
		return
	}
	policy, err := svc.store.ResolveApprovalPolicy(ctx, p.UserID, p.GrantID)
	if err != nil {
		// Fall back to demanding the step-up. Getting this wrong in the other
		// direction would offer a one-click approve the API then refuses, which
		// reads to the user as a broken button.
		policy = store.SecureApprovalPolicy()
	}
	// The client name goes into a toast title, so it is cleaned exactly as the
	// reason beside it is: both were written by a party outside this deployment.
	svc.notifier.EmitToUser(p.UserID, notify.MCPActionRequested(
		req.ID, srv.ID, srv.Name, req.Action, cleanName(p.ClientName),
		clean(req.Reason, maxEvidenceChars),
		req.ExpiresAt.UTC().Format(time.RFC3339), policy.RequirePassword))
}

// notifyActionResolved announces the end state of a request, whoever decided
// it. It does double duty: it takes down a prompt still showing on another
// device — a browser that never sees this keeps offering buttons that can only
// fail — and it is how an operator finds out an auto-approved action ran.
//
// automatic is derived from the decider rather than passed in, so the alert
// cannot disagree with the audit log about whether a human was involved.
func (svc *Service) notifyActionResolved(ctx context.Context, req *store.MCPActionRequest, clientName, deciderID string) {
	if svc.notifier == nil || req == nil {
		return
	}
	srv, err := svc.store.GetServer(ctx, req.ServerID)
	serverName := ""
	if err == nil {
		serverName = srv.Name
	}
	svc.notifier.EmitToUser(req.UserID, notify.MCPActionResolved(
		req.ID, req.ServerID, serverName, req.Action, cleanName(clientName),
		req.EffectiveStatus(svc.now()), deciderID == ""))
}

// audit writes an MCP-attributed audit entry. Best-effort by design: failing to
// log must not fail the operation, but it must be loud in the server's own log.
func (svc *Service) audit(ctx context.Context, p *Principal, serverID, action string, detail any) {
	if err := svc.store.LogActionWithActor(ctx, p.UserID, "", p.GrantID, serverID, action, "", detail); err != nil {
		slog.Error("mcp audit write failed", "action", action, "grant_id", p.GrantID, "error", err)
	}
}

// ── Executing an approved action ─────────────────────────────────

// ErrActionNotPending is returned when a request was already decided, already
// ran, or lapsed. It is the normal outcome of a double-click, a concurrent
// approval, or a retried request, and callers should treat it as such.
var ErrActionNotPending = store.ErrMCPActionNotPending

// ExecuteApprovedAction runs a request a human just approved. It is the only
// path from the MCP surface to a running Minecraft server, and everything about
// it is arranged so that path is taken at most once per request:
//
//  1. Claim. A conditional UPDATE moves the row pending → executing. Exactly
//     one caller sees one affected row; every other approval, retry, or poll
//     loses here and gets ErrActionNotPending.
//  2. Re-authorize. Only after winning the claim do we re-check the grant and
//     the owner's live permission for this specific action. Approval is not a
//     token: a grant revoked, a server dropped from the allowlist, or a
//     permission removed between filing and approving stops the action even
//     though a human just pressed the button.
//  3. Act, then settle the row as executed or failed.
//
// The claim happens before the re-check so that a failed check leaves a settled
// `failed` row rather than a `pending` one that could be approved again.
//
// deciderID is the human who approved; it is already recorded on the row by the
// claim and is passed here only for audit attribution.
func (svc *Service) ExecuteApprovedAction(ctx context.Context, requestID, userID, deciderID, ip string) (*store.MCPActionRequest, error) {
	req, err := svc.store.ClaimMCPActionRequest(ctx, requestID, userID, deciderID)
	if err != nil {
		return nil, err
	}

	grant, err := svc.store.GetMCPGrantForUser(ctx, req.GrantID, req.UserID)
	if err != nil {
		return svc.failAction(ctx, req, deciderID, ip, "the delegation behind this request no longer exists")
	}
	principal := &Principal{
		UserID:     grant.UserID,
		GrantID:    grant.ID,
		ClientName: grant.ClientName,
		Scopes:     grant.Scopes,
		ServerIDs:  grant.ServerIDs,
	}
	permission, ok := store.MCPActionPermission(req.Action)
	if !ok {
		return svc.failAction(ctx, req, deciderID, ip, "unsupported action")
	}
	srv, err := svc.authorize(ctx, principal, req.ServerID, store.MCPScopeActionsRequest, permission, false)
	if err != nil {
		return svc.failAction(ctx, req, deciderID, ip,
			"the delegation or the owner's permission no longer covers this action")
	}

	// An empty deciderID means policy let this through with nobody looking. It
	// gets its own audit action rather than sharing "approved", so a reader of
	// the log is never told a human decided something no human saw.
	approvalAction := "mcp.action.approved"
	if deciderID == "" {
		approvalAction = "mcp.action.auto_approved"
	}
	svc.auditWithIP(ctx, principal, deciderID, srv.ID, approvalAction, ip, map[string]any{
		"request_id": req.ID,
		"action":     req.Action,
		"client":     cleanName(grant.ClientName),
	})

	if err := svc.runAction(ctx, srv, req.Action); err != nil {
		slog.Error("mcp action execution failed", "request_id", req.ID, "server_id", srv.ID, "action", req.Action, "error", err)
		// The node's error text can name hosts and paths, so the stored reason
		// is ours, not the transport's.
		return svc.failAction(ctx, req, deciderID, ip, "the node did not accept the "+req.Action+" request")
	}

	if err := svc.store.FinishMCPActionRequest(ctx, req.ID, ""); err != nil {
		slog.Error("mcp action finish failed", "request_id", req.ID, "error", err)
	}
	svc.auditWithIP(ctx, principal, deciderID, srv.ID, "mcp.action.executed", ip, map[string]any{
		"request_id": req.ID,
		"action":     req.Action,
		"client":     cleanName(grant.ClientName),
	})
	settled, err := svc.store.GetMCPActionRequest(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	svc.notifyActionResolved(ctx, settled, grant.ClientName, deciderID)
	return settled, nil
}

// autoApproveIfPolicyAllows runs a freshly filed request immediately when the
// owner's policy says this class of action needs no human.
//
// It deliberately reuses ExecuteApprovedAction rather than shortcutting to
// runAction: the claim, the post-claim re-authorization, and the settle-or-fail
// bookkeeping are exactly as load-bearing here as on the human path, and an
// auto-approved action that skipped them would be the one path into a node that
// never re-checked the grant. The empty deciderID is what marks it as
// unattended, all the way down to a NULL decided_by.
//
// A failure to auto-approve is not a failure to file: the request stays in the
// queue as a normal pending item for a human to decide, so the worst case is
// the behavior the owner had before they turned the toggle on.
func (svc *Service) autoApproveIfPolicyAllows(ctx context.Context, p *Principal, req *store.MCPActionRequest) *store.MCPActionRequest {
	policy, err := svc.store.ResolveApprovalPolicy(ctx, p.UserID, p.GrantID)
	if err != nil {
		slog.Error("mcp auto-approve: policy read failed", "grant_id", p.GrantID, "error", err)
		return req
	}
	if !policy.AutoApproves(req.Action) {
		return req
	}
	settled, err := svc.ExecuteApprovedAction(ctx, req.ID, p.UserID, "", "")
	if err != nil {
		slog.Error("mcp auto-approve failed", "request_id", req.ID, "action", req.Action, "error", err)
		return req
	}
	return settled
}

// runAction performs the one lifecycle call. The switch is exhaustive over the
// three supported actions and has no default that could reach anything else.
func (svc *Service) runAction(ctx context.Context, srv *store.Server, action string) error {
	if target, ok := store.ParseMCPUpgradeAction(action); ok {
		if svc.migrations == nil {
			return errors.New("version upgrades unavailable")
		}
		_, err := svc.migrations.Trigger(context.WithoutCancel(ctx), srv.ID, target)
		return err
	}
	client, err := svc.nodes(ctx, srv)
	if err != nil {
		return fmt.Errorf("resolve node: %w", err)
	}
	switch action {
	case "start":
		// Detached from the request context: a first start may auto-install a
		// runtime for minutes, and the approving browser tab closing must not
		// abort it. Mirrors the HTTP start handler.
		startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 16*time.Minute)
		defer cancel()
		if err := svc.store.UpdateServerStatus(startCtx, srv.ID, "starting"); err != nil {
			slog.Warn("mcp action: status write failed", "server_id", srv.ID, "error", err)
		}
		if err := client.StartServer(startCtx, srv.ID, agent.StartConfigForServer(srv)); err != nil {
			if serr := svc.store.UpdateServerStatus(startCtx, srv.ID, "offline"); serr != nil {
				slog.Warn("mcp action: status rollback failed", "server_id", srv.ID, "error", serr)
			}
			return err
		}
		return nil
	case "stop":
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
		defer cancel()
		if err := client.StopServer(stopCtx, srv.ID, true, 30); err != nil {
			return err
		}
		if err := svc.store.UpdateServerStatus(stopCtx, srv.ID, "stopping"); err != nil {
			slog.Warn("mcp action: status write failed", "server_id", srv.ID, "error", err)
		}
		return nil
	case "restart":
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		// Send the current configuration, so a setting changed since the last
		// start is applied — same reasoning as the HTTP restart handler.
		return client.RestartServer(restartCtx, srv.ID, agent.StartConfigForServer(srv))
	}
	return errors.New("unsupported action")
}

// failAction settles a claimed request as failed and audits it. The request is
// already out of `pending`, so it can never be approved a second time.
func (svc *Service) failAction(ctx context.Context, req *store.MCPActionRequest, deciderID, ip, reason string) (*store.MCPActionRequest, error) {
	if err := svc.store.FinishMCPActionRequest(ctx, req.ID, reason); err != nil {
		slog.Error("mcp action failure write", "request_id", req.ID, "error", err)
	}
	if err := svc.store.LogActionWithActor(ctx, req.UserID, "", req.GrantID, req.ServerID,
		"mcp.action.failed", ip, map[string]any{
			"request_id":  req.ID,
			"action":      req.Action,
			"decided_by":  deciderID,
			"failure":     reason,
			"grant_valid": false,
		}); err != nil {
		slog.Error("mcp audit write failed", "action", "mcp.action.failed", "grant_id", req.GrantID, "error", err)
	}
	settled, err := svc.store.GetMCPActionRequest(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return settled, nil
}

// DenyAction records a human declining a request and audits the decision.
func (svc *Service) DenyAction(ctx context.Context, requestID, userID, deciderID, ip string) error {
	req, err := svc.store.GetMCPActionRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if err := svc.store.DenyMCPActionRequest(ctx, requestID, userID, deciderID); err != nil {
		return err
	}
	if err := svc.store.LogActionWithActor(ctx, req.UserID, "", req.GrantID, req.ServerID,
		"mcp.action.denied", ip, map[string]any{
			"request_id": req.ID,
			"action":     req.Action,
			"decided_by": deciderID,
		}); err != nil {
		slog.Error("mcp audit write failed", "action", "mcp.action.denied", "grant_id", req.GrantID, "error", err)
	}
	if settled, err := svc.store.GetMCPActionRequest(ctx, requestID); err == nil {
		svc.notifyActionResolved(ctx, settled, "", deciderID)
	}
	return nil
}

func (svc *Service) auditWithIP(ctx context.Context, p *Principal, deciderID, serverID, action, ip string, detail map[string]any) {
	detail["decided_by"] = deciderID
	if err := svc.store.LogActionWithActor(ctx, p.UserID, "", p.GrantID, serverID, action, ip, detail); err != nil {
		slog.Error("mcp audit write failed", "action", action, "grant_id", p.GrantID, "error", err)
	}
}
