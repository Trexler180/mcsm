package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/mcpserver"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// MCPGrantHandlers is the owner's side of remote agent access: see what is
// connected, cut it off, and decide the actions an agent has asked for.
//
// Every route is human-only. The delegation model only means anything if the
// human stays in the loop, so no machine credential — access key or MCP token —
// can read this surface, and none can approve an action. An agent that could
// approve its own request would have turned the queue into a formality.
type MCPGrantHandlers struct {
	store *store.Store
	mcp   *mcpserver.Service
	urls  publicurl.Config

	// Approving an action is a step-up, so it spends from the same throttles
	// as login and every other password check.
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
}

// NewMCPGrantHandlers builds the delegation handlers. pw is the password-guess
// budget shared with login and the other step-ups; nil gives a private one.
func NewMCPGrantHandlers(s *store.Store, urls publicurl.Config, pw *PasswordThrottles, opts ...mcpserver.Option) *MCPGrantHandlers {
	pw = pw.orNew()
	return &MCPGrantHandlers{
		store:        s,
		mcp:          mcpserver.New(s, opts...),
		urls:         urls,
		ipThrottle:   pw.IP,
		acctThrottle: pw.Account,
	}
}

// ── Grants ───────────────────────────────────────────────────────

// grantView is a grant as the dashboard shows it. It is deliberately a
// projection rather than the store type: there is no field here that could
// carry token material, because no such field exists on it.
type grantView struct {
	ID         string     `json:"id"`
	ClientName string     `json:"client_name"`
	Scopes     []string   `json:"scopes"`
	Servers    []grantSrv `json:"servers"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastUsedIP *string    `json:"last_used_ip"`
	Active     bool       `json:"active"`

	// The connection's own policy overrides (null = inherit) alongside what
	// those overrides actually resolve to once the account defaults are
	// applied. Sending both means the UI can render a tri-state control and
	// still tell the user what is in force, without reimplementing precedence.
	Overrides store.GrantPolicyOverrides `json:"overrides"`
	Policy    store.ApprovalPolicy       `json:"policy"`
}

type grantSrv struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListGrants returns the caller's delegations, including revoked and expired
// ones so the owner keeps a record of what existed and when it stopped.
func (h *MCPGrantHandlers) ListGrants(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	grants, err := h.store.ListMCPGrants(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "list mcp grants", err)
		return
	}
	base, err := h.store.GetUserApprovalSettings(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "read mcp approval settings", err)
		return
	}
	now := time.Now()
	out := make([]grantView, 0, len(grants))
	for _, g := range grants {
		view := grantView{
			ID: g.ID,
			// Same cleaning as the approval queue and the consent screen. This
			// name was chosen by whoever registered the client, and the three
			// surfaces that show it to a human have to agree about that.
			ClientName: mcpserver.CleanUntrusted(g.ClientName),
			Scopes:     g.Scopes,
			Servers:    make([]grantSrv, 0, len(g.ServerIDs)),
			CreatedAt:  g.CreatedAt,
			ExpiresAt:  g.ExpiresAt,
			RevokedAt:  g.RevokedAt,
			LastUsedAt: g.LastUsedAt,
			LastUsedIP: g.LastUsedIP,
			Active:     g.Active(now),
			Overrides: store.GrantPolicyOverrides{
				RequirePassword:      g.RequirePassword,
				AutoApproveLifecycle: g.AutoApproveLifecycle,
				AutoApproveUpgrades:  g.AutoApproveUpgrades,
			},
			Policy: store.ApplyGrantOverrides(base, g),
		}
		for _, id := range g.ServerIDs {
			name := ""
			if srv, err := h.store.GetServer(r.Context(), id); err == nil {
				name = srv.Name
			}
			view.Servers = append(view.Servers, grantSrv{ID: id, Name: name})
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}

// RevokeGrant cuts a delegation off immediately: the grant, every access token,
// every refresh family, and any code not yet redeemed.
//
// No step-up is required, deliberately. Revocation only ever removes authority,
// and putting a password prompt between a worried operator and the stop button
// would be a security control that makes the incident worse. Idempotent, so a
// panicked double-click is a 204 both times.
func (h *MCPGrantHandlers) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.store.RevokeMCPGrant(r.Context(), id, uid); err != nil {
		if errors.Is(err, store.ErrMCPGrantNotFound) {
			// A grant belonging to someone else reads exactly like one that does
			// not exist.
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		writeServerError(w, r, "revoke mcp grant", err)
		return
	}
	audit(h.store, r, "", "mcp.grant.revoked", map[string]any{"grant_id": id, "reason": "revoked by owner"})
	w.WriteHeader(http.StatusNoContent)
}

// ── Action approvals ─────────────────────────────────────────────

// actionView is a pending or settled request as the approver sees it.
type actionView struct {
	ID         string `json:"id"`
	GrantID    string `json:"grant_id"`
	ClientName string `json:"client_name"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	Action     string `json:"action"`
	// Reason is written by the model. The UI renders it as untrusted text and
	// the panel never interprets it; it is here so a human can judge whether
	// the stated justification matches what they can see for themselves.
	Reason        string     `json:"reason"`
	Untrusted     bool       `json:"untrusted"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	DecidedAt     *time.Time `json:"decided_at"`
	ExecutedAt    *time.Time `json:"executed_at"`
	FailureReason *string    `json:"failure_reason"`
	// RequiresPassword tells the client whether approving this request needs a
	// step-up, so the notification can offer a one-click approve where the
	// policy allows one without the browser having to resolve precedence for
	// itself. The server re-checks regardless: this field decides which form to
	// draw, never whether the password is actually verified.
	RequiresPassword bool `json:"requires_password"`
}

func toActionView(a *store.MCPActionRequest, now time.Time, requiresPassword bool) actionView {
	view := actionView{
		ID:         a.ID,
		GrantID:    a.GrantID,
		ClientName: mcpserver.CleanUntrusted(a.ClientName),
		ServerID:   a.ServerID,
		ServerName: a.ServerName,
		Action:     a.Action,
		// Cleaned on the way out, exactly as the notification path cleans it.
		// This is the other surface where a human reads the model's own words
		// and decides, so it cannot be the lenient one: the same string arriving
		// redacted in a toast and raw in the settings list is a boundary with a
		// hole in whichever half someone forgets.
		Reason:           mcpserver.CleanUntrusted(a.Reason),
		Untrusted:        true,
		Status:           a.EffectiveStatus(now),
		CreatedAt:        a.CreatedAt,
		ExpiresAt:        a.ExpiresAt,
		DecidedAt:        a.DecidedAt,
		ExecutedAt:       a.ExecutedAt,
		RequiresPassword: requiresPassword,
	}
	if a.FailureReason != nil {
		cleaned := mcpserver.CleanUntrusted(*a.FailureReason)
		view.FailureReason = &cleaned
	}
	return view
}

// ListActionRequests returns the caller's queue, newest first.
func (h *MCPGrantHandlers) ListActionRequests(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	// Settle anything that lapsed before building the list, so the queue a human
	// reads never offers a button that could only fail. EffectiveStatus already
	// reports expiry correctly on the way out; this is what makes the stored
	// status agree with it.
	if err := h.store.ExpireStaleMCPActionRequests(r.Context()); err != nil {
		slog.Warn("mcp: expiring stale action requests failed", "error", err)
	}
	requests, err := h.store.ListMCPActionRequestsForUser(r.Context(), uid, 50)
	if err != nil {
		writeServerError(w, r, "list mcp action requests", err)
		return
	}
	now := time.Now()
	out := make([]actionView, 0, len(requests))
	for _, a := range requests {
		policy, err := h.store.ResolveApprovalPolicy(r.Context(), uid, a.GrantID)
		if err != nil {
			writeServerError(w, r, "resolve mcp approval policy", err)
			return
		}
		out = append(out, toActionView(a, now, policy.RequirePassword))
	}
	writeJSON(w, http.StatusOK, out)
}

type approvalRequest struct {
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// ApproveAction is the only path from an agent's request to a running server,
// and it is gated on a fresh human step-up.
//
// The work itself lives in the facade's ExecuteApprovedAction, which claims the
// request with a conditional UPDATE before doing anything — so a double-click,
// a retried request, and two operators approving at once all resolve to exactly
// one execution. It also re-checks the grant and the owner's live permission
// *after* claiming, because a human pressing approve is not a substitute for
// authority the delegation may have lost since the request was filed.
func (h *MCPGrantHandlers) ApproveAction(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body approvalRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	id := chi.URLParam(r, "id")
	// Scope the lookup to the owner before anything else: a request id belonging
	// to someone else must be indistinguishable from one that does not exist.
	req, err := h.store.GetMCPActionRequest(r.Context(), id)
	if err != nil || req.UserID != uid {
		writeError(w, http.StatusNotFound, "action request not found")
		return
	}

	// Whether this needs a step-up is a property of the grant the request was
	// filed under, resolved here rather than trusted from the client. Turning
	// the step-up off is itself a change that required one (see
	// UpdateApprovalSettings), so this is the second half of a decision the
	// owner already re-proved their identity to make.
	policy, err := h.store.ResolveApprovalPolicy(r.Context(), uid, req.GrantID)
	if err != nil {
		writeServerError(w, r, "resolve mcp approval policy", err)
		return
	}
	if policy.RequirePassword && !h.reauthenticate(w, r, uid, body) {
		return
	}

	settled, err := h.mcp.ExecuteApprovedAction(r.Context(), id, uid, uid, clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, mcpserver.ErrActionNotPending):
			writeError(w, http.StatusConflict, "this request is no longer pending")
		case errors.Is(err, store.ErrMCPActionNotFound):
			writeError(w, http.StatusNotFound, "action request not found")
		default:
			writeServerError(w, r, "approve mcp action", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, toActionView(settled, time.Now(), policy.RequirePassword))
}

// DenyAction records a refusal. No step-up: declining removes nothing and
// grants nothing, and friction on "no" is friction in the wrong direction.
func (h *MCPGrantHandlers) DenyAction(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	req, err := h.store.GetMCPActionRequest(r.Context(), id)
	if err != nil || req.UserID != uid {
		writeError(w, http.StatusNotFound, "action request not found")
		return
	}
	if err := h.mcp.DenyAction(r.Context(), id, uid, uid, clientIP(r)); err != nil {
		switch {
		case errors.Is(err, store.ErrMCPActionNotPending):
			writeError(w, http.StatusConflict, "this request is no longer pending")
		case errors.Is(err, store.ErrMCPActionNotFound):
			writeError(w, http.StatusNotFound, "action request not found")
		default:
			writeServerError(w, r, "deny mcp action", err)
		}
		return
	}
	settled, err := h.store.GetMCPActionRequest(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "deny mcp action", err)
		return
	}
	// A settled request is never approved again, so the step-up flag on the way
	// out is informational only.
	writeJSON(w, http.StatusOK, toActionView(settled, time.Now(), false))
}

// ── Approval policy ──────────────────────────────────────────────

// approvalSettingsRequest carries the desired policy plus the credentials for
// the step-up that relaxing it demands. The credentials are ignored when the
// change only tightens.
type approvalSettingsRequest struct {
	store.ApprovalPolicy
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// GetApprovalSettings returns the caller's account-level defaults.
func (h *MCPGrantHandlers) GetApprovalSettings(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	policy, err := h.store.GetUserApprovalSettings(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "read mcp approval settings", err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// UpdateApprovalSettings writes the caller's account-level defaults.
//
// Relaxing any dimension — dropping the approval step-up, or letting either
// class of action run unattended — requires a fresh password/TOTP step-up.
// Without that, anyone holding a live session could switch the protection off
// and then approve whatever they liked, which would make the step-up
// decorative. Tightening is free, and deliberately so: no security control
// should put friction in front of turning it back on.
func (h *MCPGrantHandlers) UpdateApprovalSettings(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body approvalSettingsRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	current, err := h.store.GetUserApprovalSettings(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "read mcp approval settings", err)
		return
	}
	if current.Relaxes(body.ApprovalPolicy) &&
		!h.reauthenticate(w, r, uid, approvalRequest{Password: body.Password, TOTPCode: body.TOTPCode}) {
		return
	}
	if err := h.store.SetUserApprovalSettings(r.Context(), uid, body.ApprovalPolicy); err != nil {
		writeServerError(w, r, "write mcp approval settings", err)
		return
	}
	audit(h.store, r, "", "mcp.approval_settings.updated", map[string]any{
		"require_password":       body.RequirePassword,
		"auto_approve_lifecycle": body.AutoApproveLifecycle,
		"auto_approve_upgrades":  body.AutoApproveUpgrades,
		"relaxed":                current.Relaxes(body.ApprovalPolicy),
	})
	writeJSON(w, http.StatusOK, body.ApprovalPolicy)
}

// grantOverridesRequest is the per-connection layer. Every policy field is
// tri-state: omitting it or sending null means "inherit the account default".
type grantOverridesRequest struct {
	store.GrantPolicyOverrides
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// UpdateGrantApprovalSettings writes one connection's overrides, with the same
// relax-needs-a-step-up rule as the account defaults. The comparison is made
// against what the connection resolves to today, not against the raw override,
// so clearing an override that was tightening the account default still counts
// as a relaxation.
func (h *MCPGrantHandlers) UpdateGrantApprovalSettings(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	var body grantOverridesRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := h.store.GetMCPGrantForUser(r.Context(), id, uid); err != nil {
		writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	current, err := h.store.ResolveApprovalPolicy(r.Context(), uid, id)
	if err != nil {
		writeServerError(w, r, "resolve mcp approval policy", err)
		return
	}
	base, err := h.store.GetUserApprovalSettings(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "read mcp approval settings", err)
		return
	}
	next := store.ApplyGrantOverrides(base, &store.MCPGrant{
		RequirePassword:      body.RequirePassword,
		AutoApproveLifecycle: body.AutoApproveLifecycle,
		AutoApproveUpgrades:  body.AutoApproveUpgrades,
	})
	if current.Relaxes(next) &&
		!h.reauthenticate(w, r, uid, approvalRequest{Password: body.Password, TOTPCode: body.TOTPCode}) {
		return
	}
	if err := h.store.SetGrantPolicyOverrides(r.Context(), id, uid, body.GrantPolicyOverrides); err != nil {
		if errors.Is(err, store.ErrMCPGrantNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		writeServerError(w, r, "write mcp grant overrides", err)
		return
	}
	audit(h.store, r, "", "mcp.grant.approval_settings.updated", map[string]any{
		"grant_id": id,
		"relaxed":  current.Relaxes(next),
	})
	writeJSON(w, http.StatusOK, next)
}

// reauthenticate enforces the password + enabled-TOTP step-up, with the same
// per-IP and per-account throttles the access-key handlers use. Every failure
// is the same generic 401, so the response cannot distinguish a wrong password
// from a wrong code.
func (h *MCPGrantHandlers) reauthenticate(w http.ResponseWriter, r *http.Request, userID string, body approvalRequest) bool {
	ipKey := passwordIPKey(r)
	acctKey := stepUpAccountKey(userID)
	if ok, retry := h.ipThrottle.Allowed(ipKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	if ok, retry := h.acctThrottle.Allowed(acctKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	fail := func() {
		h.ipThrottle.Fail(ipKey)
		h.acctThrottle.Fail(acctKey)
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
	}

	if body.Password == "" {
		// An empty password is a failed attempt, not a free probe.
		fail()
		return false
	}
	hash, err := h.store.GetUserPasswordHash(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
		return false
	}
	if !auth.CheckPassword(hash, body.Password) {
		fail()
		return false
	}
	cfg, err := h.store.GetUserTOTP(r.Context(), userID)
	if err != nil {
		writeServerError(w, r, "mcp approval: totp lookup", err)
		return false
	}
	if cfg.Enabled && !auth.ValidateTOTP(cfg.Secret, body.TOTPCode, time.Now()) {
		fail()
		return false
	}

	h.ipThrottle.Reset(ipKey)
	h.acctThrottle.Reset(acctKey)
	return true
}

// ── Connect instructions ─────────────────────────────────────────

// connectionInfo is what the Connect panel renders.
//
// Note what is absent: there is no token, and no field that could hold one.
// The entire point of this feature is that connecting a client requires a
// browser click rather than a secret pasted into a config file, so a snippet
// carrying a credential would defeat it — and would end up committed to
// somebody's dotfiles repository.
type connectionInfo struct {
	Configured   bool     `json:"configured"`
	URL          string   `json:"url"`
	ClaudeCode   string   `json:"claude_code"`
	CodexConfig  string   `json:"codex_config"`
	HermesConfig string   `json:"hermes_config"`
	Scopes       []string `json:"scopes"`
	MaxDays      int      `json:"max_days"`
	DefaultDays  int      `json:"default_days"`
}

// ConnectionInfo returns the exact commands for connecting a client.
func (h *MCPGrantHandlers) ConnectionInfo(w http.ResponseWriter, r *http.Request) {
	if uid := humanCaller(w, r); uid == "" {
		return
	}
	info := connectionInfo{
		Scopes:      store.AllMCPScopes(),
		MaxDays:     int(store.MaxMCPGrantLifetime / (24 * time.Hour)),
		DefaultDays: int(store.DefaultMCPGrantLifetime / (24 * time.Hour)),
	}
	resource, err := h.urls.MCPResource(r)
	if err != nil {
		// Not an error to the caller: an unconfigured deployment is a normal
		// state, and the panel explains what to set rather than failing.
		writeJSON(w, http.StatusOK, info)
		return
	}
	info.Configured = true
	info.URL = resource
	info.ClaudeCode = "claude mcp add --transport http servermanager " + resource
	info.CodexConfig = strings.Join([]string{
		"[mcp_servers.servermanager]",
		`url = "` + resource + `"`,
	}, "\n")
	// Hermes reads MCP servers from ~/.hermes/config.yaml. `auth: oauth` is what
	// makes it run the same browser flow the other two clients use, rather than
	// looking for a header or a token in the file — which is the whole point of
	// this panel, so it is not optional here.
	info.HermesConfig = strings.Join([]string{
		"mcp_servers:",
		"  servermanager:",
		`    url: "` + resource + `"`,
		"    auth: oauth",
	}, "\n")
	writeJSON(w, http.StatusOK, info)
}
