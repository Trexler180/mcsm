package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/mcpserver"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// MCPOAuthHandlers is the OAuth 2.1 authorization server that fronts the MCP
// resource. It exists so an MCP client can be connected with one command and a
// browser click, and so the credential that results is scoped, expiring, and
// revocable — rather than a long-lived token pasted into a config file.
//
// Everything here assumes a *public* client with PKCE. Claude Code and Codex
// run on the operator's own machine and cannot keep a secret, so no endpoint
// accepts one; issuing a pretend secret would only invite treating those
// clients as confidential when they are not.
type MCPOAuthHandlers struct {
	store *store.Store
	urls  publicurl.Config

	// allowRemoteRedirects opens dynamic registration up to callbacks that are
	// not on the operator's own machine. Off by default: see
	// validateRedirectURIs for why that default is the security-relevant one.
	allowRemoteRedirects bool
}

func NewMCPOAuthHandlers(s *store.Store, urls publicurl.Config) *MCPOAuthHandlers {
	return &MCPOAuthHandlers{
		store:                s,
		urls:                 urls,
		allowRemoteRedirects: strings.EqualFold(strings.TrimSpace(os.Getenv("MCP_ALLOW_REMOTE_REDIRECTS")), "true"),
	}
}

// consentRoute is the SPA route that renders the approval screen.
const consentRoute = "mcp-consent"

// Bounds on what a client may send. Every one of these is a cap on unauthenticated
// input, so they are deliberately tight: nothing legitimate approaches them.
const (
	maxRedirectURIs   = 5
	maxRedirectURILen = 512
	// Client name and software id are bounded by the store, on rune boundaries;
	// see store.RegisterMCPClient.
	maxStateLen          = 512
	maxCodeChallengeLen  = 128
	minCodeChallengeLen  = 43
	maxRegisterBodyBytes = 8 << 10
)

// ── Metadata ─────────────────────────────────────────────────────

// ProtectedResourceMetadata answers RFC 9728 discovery. A client that gets a
// 401 from the MCP endpoint reads the `WWW-Authenticate` header, fetches this,
// and learns which authorization server to talk to.
func (h *MCPOAuthHandlers) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.origin(w, r)
	if !ok {
		return
	}
	h.writeMetadata(w, map[string]any{
		"resource":                              origin + publicurl.MCPResourcePath,
		"authorization_servers":                 []string{origin},
		"scopes_supported":                      store.AllMCPScopes(),
		"bearer_methods_supported":              []string{"header"},
		"resource_name":                         "ServerManager",
		"resource_documentation":                origin + "/docs/mcp",
		"authorization_details_types_supported": []string{},
	})
}

// AuthorizationServerMetadata answers RFC 8414 discovery.
//
// The advertised capabilities are the ones actually implemented, and no more:
// `code` only, `S256` only, and `none` for token-endpoint auth. Advertising a
// method the server does not support is how clients end up negotiating a weaker
// flow than the one that was designed.
func (h *MCPOAuthHandlers) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.origin(w, r)
	if !ok {
		return
	}
	h.writeMetadata(w, map[string]any{
		"issuer":                                     origin,
		"authorization_endpoint":                     origin + "/api/v1/oauth/authorize",
		"token_endpoint":                             origin + "/api/v1/oauth/token",
		"registration_endpoint":                      origin + "/api/v1/oauth/register",
		"revocation_endpoint":                        origin + "/api/v1/oauth/revoke",
		"scopes_supported":                           store.AllMCPScopes(),
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"},
		"service_documentation":                      origin + "/docs/mcp",
	})
}

// writeMetadata emits a discovery document. These are public by definition —
// a client must be able to read them before it holds any credential — so they
// are CORS-open and briefly cacheable.
func (h *MCPOAuthHandlers) writeMetadata(w http.ResponseWriter, doc map[string]any) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, doc)
}

// MetadataPreflight answers the CORS preflight a browser-based client sends
// before reading discovery documents.
func (h *MCPOAuthHandlers) MetadataPreflight(w http.ResponseWriter, r *http.Request) {
	head := w.Header()
	head.Set("Access-Control-Allow-Origin", "*")
	head.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	head.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	head.Set("Access-Control-Max-Age", "300")
	w.WriteHeader(http.StatusNoContent)
}

// origin resolves the deployment's public origin or fails closed.
//
// Guessing here would be worse than refusing: every token this server mints is
// bound to a resource identifier built from this value, so a wrong answer
// produces credentials for an audience nobody intended.
func (h *MCPOAuthHandlers) origin(w http.ResponseWriter, r *http.Request) (string, bool) {
	origin, err := h.urls.Origin(r)
	if err != nil {
		slog.Error("mcp: public origin unavailable",
			"error", err,
			"hint", "set MCP_PUBLIC_ORIGIN or APP_ORIGIN to this deployment's external https origin")
		writeError(w, http.StatusServiceUnavailable, "remote agent access is not configured on this deployment")
		return "", false
	}
	return origin, true
}

// ── Authorize ────────────────────────────────────────────────────

// Authorize validates an authorization request and parks it for a human.
//
// The order of checks is the security-relevant part. Client identity and the
// redirect URI are settled *first*, because every later error is reported by
// redirecting to that URI — so it must be one the client genuinely registered
// before it can be used as a sink for anything. An unknown client or an
// unmatched redirect URI therefore renders a page and never redirects. That
// distinction is the open-redirect boundary.
func (h *MCPOAuthHandlers) Authorize(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.origin(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	clientID := strings.TrimSpace(q.Get("client_id"))
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	if clientID == "" || redirectURI == "" {
		h.authorizeErrorPage(w, "This request is missing the client or redirect information it needs.")
		return
	}
	client, err := h.store.GetMCPClient(r.Context(), clientID)
	if err != nil {
		// Deliberately not a redirect: we have no verified place to send this.
		h.authorizeErrorPage(w, "This application is not registered with ServerManager.")
		return
	}
	if !client.AllowsRedirect(redirectURI) {
		h.authorizeErrorPage(w, "This application asked to be sent to an address it has not registered.")
		return
	}

	state := q.Get("state")
	if len(state) > maxStateLen {
		h.authorizeRedirectError(w, r, redirectURI, state, "invalid_request", "state is too long")
		return
	}

	// From here on the redirect URI is verified, so OAuth errors go back to the
	// client the way the spec expects.
	if q.Get("response_type") != "code" {
		h.authorizeRedirectError(w, r, redirectURI, state, "unsupported_response_type", "only the authorization code flow is supported")
		return
	}
	if q.Get("code_challenge_method") != "S256" {
		h.authorizeRedirectError(w, r, redirectURI, state, "invalid_request", "code_challenge_method must be S256")
		return
	}
	challenge := strings.TrimSpace(q.Get("code_challenge"))
	if len(challenge) < minCodeChallengeLen || len(challenge) > maxCodeChallengeLen {
		h.authorizeRedirectError(w, r, redirectURI, state, "invalid_request", "a PKCE code_challenge is required")
		return
	}

	// The resource parameter is what binds the resulting token to this exact MCP
	// endpoint. Requiring it (rather than defaulting it) means a token can never
	// acquire an audience by accident.
	resource, err := h.canonicalResource(r, q.Get("resource"), origin)
	if err != nil {
		h.authorizeRedirectError(w, r, redirectURI, state, "invalid_target", "the resource parameter must name this server's MCP endpoint")
		return
	}

	scopes, err := store.NormalizeMCPScopes(strings.Fields(q.Get("scope")))
	if err != nil {
		if errors.Is(err, store.ErrMCPNoScopes) {
			// A client that asks for nothing in particular gets the read-only set
			// offered on the consent screen, where the human narrows it further.
			scopes = defaultConsentScopes()
		} else {
			h.authorizeRedirectError(w, r, redirectURI, state, "invalid_scope", "one of the requested capabilities is not supported")
			return
		}
	}

	req, err := h.store.CreateMCPAuthorizationRequest(r.Context(), &store.MCPAuthorizationRequest{
		ClientID:            client.ClientID,
		RedirectURI:         redirectURI,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Scopes:              scopes,
		Resource:            resource,
	})
	if err != nil {
		slog.Error("mcp: parking authorization request failed", "client_id", client.ClientID, "error", err)
		h.authorizeRedirectError(w, r, redirectURI, state, "server_error", "the request could not be started")
		return
	}

	consent, err := h.urls.SPAURL(r, consentRoute)
	if err != nil {
		slog.Error("mcp: consent URL unavailable", "error", err)
		h.authorizeRedirectError(w, r, redirectURI, state, "server_error", "the consent screen is not reachable")
		return
	}
	target, err := url.Parse(consent)
	if err != nil {
		h.authorizeRedirectError(w, r, redirectURI, state, "server_error", "the consent screen is not reachable")
		return
	}
	// The request id is the only thing handed to the browser. Everything the
	// consent screen shows is read back from the parked row, so nothing the
	// client put in the URL can be changed between here and approval.
	target.RawQuery = url.Values{"request": {req.ID}}.Encode()

	noStore(w)
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// canonicalResource verifies that the client is asking for *this* MCP endpoint.
func (h *MCPOAuthHandlers) canonicalResource(r *http.Request, raw, origin string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("resource is required")
	}
	canonical, err := publicurl.CanonicalizeResource(raw)
	if err != nil {
		return "", err
	}
	expected := origin + publicurl.MCPResourcePath
	if !publicurl.SameResource(canonical, expected) {
		return "", errors.New("resource does not match this server")
	}
	// Always the server's own spelling, never the client's. What a token is
	// bound to has to be one string, decided here.
	return expected, nil
}

// defaultConsentScopes is what a client that named no scopes is offered. It is
// the read-only set: requesting an action is a capability a human should have
// to deliberately add, not one that arrives by omission.
func defaultConsentScopes() []string {
	return []string{
		string(store.MCPScopeServersRead),
		string(store.MCPScopeDiagnosticsRead),
		string(store.MCPScopeLogsRead),
		string(store.MCPScopeMetricsRead),
		string(store.MCPScopeAuditRead),
	}
}

// authorizeRedirectError reports an OAuth error to an already-validated
// redirect URI.
func (h *MCPOAuthHandlers) authorizeRedirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		h.authorizeErrorPage(w, "This application's redirect address could not be used.")
		return
	}
	q := target.Query()
	q.Set("error", code)
	q.Set("error_description", description)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	noStore(w)
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// authorizeErrorPage renders the terminal failure for a request that cannot be
// sent anywhere. It is HTML because the caller here is a browser mid-flow, and
// it is a template so the message cannot inject markup.
var authorizeErrorTemplate = template.Must(template.New("mcp-authorize-error").Parse(
	`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Connection request refused</title>` +
		`<style>body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;background:#0b0e14;color:#e6e9ef;` +
		`display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}` +
		`main{max-width:32rem;padding:2rem}h1{font-size:1.25rem;margin:0 0 .75rem}` +
		`p{line-height:1.6;color:#9aa4b2;margin:0 0 .5rem}</style></head>` +
		`<body><main><h1>ServerManager refused this connection request</h1>` +
		`<p>{{.}}</p><p>Nothing was approved and no access was granted. ` +
		`You can close this window.</p></main></body></html>`))

func (h *MCPOAuthHandlers) authorizeErrorPage(w http.ResponseWriter, message string) {
	noStore(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	if err := authorizeErrorTemplate.Execute(w, message); err != nil {
		slog.Error("mcp: rendering authorize error page failed", "error", err)
	}
}

// ── Consent ──────────────────────────────────────────────────────

// consentScope is one capability, described the way a human should read it
// rather than the way a scope string spells it.
type consentScope struct {
	Scope       string `json:"scope"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
}

var consentScopeCopy = map[store.MCPScope]consentScope{
	store.MCPScopeServersRead: {
		Title:       "See your servers",
		Description: "List the servers you select below, with their name, status, and version.",
	},
	store.MCPScopeDiagnosticsRead: {
		Title:       "Read diagnostics",
		Description: "Read health, resource usage, and recent errors for those servers.",
	},
	store.MCPScopeLogsRead: {
		Title:       "Read recent log events",
		Description: "Read recent indexed warnings and errors from the server console output.",
	},
	store.MCPScopeMetricsRead: {
		Title:       "Read performance history",
		Description: "Read recent CPU, memory, and player-count history.",
	},
	store.MCPScopeAuditRead: {
		Title:       "Read the server audit trail",
		Description: "Read who did what on those servers inside ServerManager.",
	},
	store.MCPScopeActionsRequest: {
		Title:       "Request lifecycle actions and version upgrades",
		Description: "File a request to start, stop, restart, or run a target-specific version upgrade with one restore-point backup. Nothing happens until you approve each request individually with password/MFA.",
		Sensitive:   true,
	},
	store.MCPScopePowerStart: {
		Title:       "Start servers directly",
		Description: "Start a selected server without another approval prompt.",
		Sensitive:   true,
	},
	store.MCPScopePowerStop: {
		Title:       "Stop servers directly",
		Description: "Gracefully stop a selected server without another approval prompt.",
		Sensitive:   true,
	},
	store.MCPScopePowerRestart: {
		Title:       "Restart servers directly",
		Description: "Restart a selected server without another approval prompt.",
		Sensitive:   true,
	},
	store.MCPScopeModsRead: {
		Title:       "Inspect and search mods",
		Description: "List installed mods, search compatible sources, and check available updates.",
	},
	store.MCPScopeModsInstall: {
		Title:       "Install mods",
		Description: "Install compatible mods and required dependencies on selected servers.",
		Sensitive:   true,
	},
	store.MCPScopeModsUpdate: {
		Title:       "Update or disable mods",
		Description: "Update, enable, or disable installed mods. Dependency-breaking changes require explicit acknowledgement.",
		Sensitive:   true,
	},
	store.MCPScopeModsRemove: {
		Title:       "Remove mods",
		Description: "Remove installed mods. Dependency-breaking removal requires explicit acknowledgement.",
		Sensitive:   true,
	},
	store.MCPScopeBackupsCreate: {
		Title:       "Request human-confirmed backups",
		Description: "Request a full backup. Every backup requires a separate human confirmation through the MCP client; other operations never imply consent.",
		Sensitive:   true,
	},
	store.MCPScopePlayersWhitelist: {
		Title:       "Add and remove whitelist entries",
		Description: "Whitelist or un-whitelist one named player on a selected server. It cannot op, ban, or kick anyone.",
		Sensitive:   true,
	},
	store.MCPScopeLogsRaw: {
		Title: "Read the raw server log and crash reports",
		Description: "Read the tail of the console log and the newest crash report for the servers you select. " +
			"This is the raw text the server wrote rather than ServerManager's summary of it, which is what lets " +
			"an agent diagnose a crash instead of guessing. It reaches those two files and no others, and writes " +
			"nothing. Values that look like passwords are removed first.",
		Sensitive: true,
	},
	store.MCPScopeConfigRead: {
		Title: "Read server.properties",
		Description: "Read the selected servers' own properties file — difficulty, whitelist enforcement, MOTD, " +
			"view distance. It reads that one file and changes nothing, and values that look like passwords are " +
			"removed first.",
		Sensitive: true,
	},
	store.MCPScopePlayersRead: {
		Title:       "See who plays on your servers",
		Description: "Read the whitelist, the ban list, and recently seen player names for the servers you select. It changes nothing.",
		Sensitive:   true,
	},
	store.MCPScopeConsoleRun: {
		Title:       "Run a fixed set of console commands",
		Description: "Run only the allowlisted console commands (say, list, kick, time, weather, difficulty, save-all, whitelist list/reload). It cannot run an arbitrary command, and each command still requires the permission its effect needs.",
		Sensitive:   true,
	},
}

// consentServer is a server the caller may delegate, with the scopes they can
// actually back on it. The screen offers exactly this and no more.
type consentServer struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

type consentView struct {
	RequestID      string          `json:"request_id"`
	ClientName     string          `json:"client_name"`
	ClientOrigin   string          `json:"client_origin"`
	RedirectHost   string          `json:"redirect_host"`
	Resource       string          `json:"resource"`
	Scopes         []consentScope  `json:"scopes"`
	Servers        []consentServer `json:"servers"`
	ExpiresAt      time.Time       `json:"expires_at"`
	DefaultDays    int             `json:"default_days"`
	MaxDays        int             `json:"max_days"`
	AlreadyDecided bool            `json:"already_decided"`
}

// Consent returns everything the approval screen must show. It is
// JWT-authenticated and refuses machine principals: a delegation is a human
// decision, and a credential must never be able to widen itself by approving
// its own successor.
func (h *MCPOAuthHandlers) Consent(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	req, client, ok := h.loadPendingRequest(w, r)
	if !ok {
		return
	}

	view := consentView{
		RequestID: req.ID,
		// Cleaned on the way out as well as on the way in. Registration strips
		// this now, but a row written before that did not, and this screen is
		// where a human decides whether to trust the name — the one surface
		// that must not be the lenient one.
		ClientName:   mcpserver.CleanUntrusted(client.ClientName),
		ClientOrigin: client.Origin,
		Resource:     req.Resource,
		ExpiresAt:    req.ExpiresAt,
		DefaultDays:  int(store.DefaultMCPGrantLifetime / (24 * time.Hour)),
		MaxDays:      int(store.MaxMCPGrantLifetime / (24 * time.Hour)),
	}
	if u, err := url.Parse(req.RedirectURI); err == nil {
		view.RedirectHost = u.Host
	}
	for _, s := range req.Scopes {
		copyFor, found := consentScopeCopy[store.MCPScope(s)]
		if !found {
			// A scope with no human description is one this build does not
			// understand; refuse rather than render a bare string a user cannot
			// evaluate.
			writeError(w, http.StatusBadRequest, "this request asks for a capability this version does not offer")
			return
		}
		copyFor.Scope = s
		view.Scopes = append(view.Scopes, copyFor)
	}

	servers, err := h.delegatableServers(r, uid, req.Scopes)
	if err != nil {
		writeServerError(w, r, "mcp consent: server list", err)
		return
	}
	view.Servers = servers

	noStore(w)
	writeJSON(w, http.StatusOK, view)
}

// delegatableServers lists the servers the caller could actually delegate, and
// for each one the subset of requested scopes they currently hold there.
//
// A user cannot delegate authority they do not have, so the screen never offers
// a server/scope pair that would be rejected at approval — and the check is
// repeated at approval anyway, because this list is a convenience and the
// re-check is the boundary.
func (h *MCPOAuthHandlers) delegatableServers(r *http.Request, userID string, scopes []string) ([]consentServer, error) {
	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		return nil, errors.New("owner not found")
	}
	servers, err := h.store.ListServersForUser(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	out := []consentServer{}
	for _, srv := range servers {
		held := []string{}
		for _, scope := range scopes {
			ok, err := h.holdsScope(r, user, srv.ID, scope)
			if err != nil {
				return nil, err
			}
			if ok {
				held = append(held, scope)
			}
		}
		if len(held) == 0 {
			continue
		}
		out = append(out, consentServer{ID: srv.ID, Name: srv.Name, Scopes: held})
	}
	return out, nil
}

// holdsScope reports whether a user currently holds what a scope requires on a
// server. A global admin satisfies the RBAC term exactly as they do on the HTTP
// routes — which lets an admin delegate on a server they administer without
// widening the grant, whose bounds stay the servers and scopes recorded on it.
func (h *MCPOAuthHandlers) holdsScope(r *http.Request, user *store.User, serverID, scope string) (bool, error) {
	permission, group, ok := store.MCPScopeRequirement(scope)
	if !ok {
		return false, nil
	}
	if user.Role == "admin" {
		return true, nil
	}
	if group {
		return h.store.UserHasServerGroupAccess(r.Context(), user.ID, serverID, permission)
	}
	return h.store.UserHasServerPermission(r.Context(), user.ID, serverID, permission)
}

type consentDecision struct {
	RequestID string   `json:"request_id"`
	Approve   bool     `json:"approve"`
	Scopes    []string `json:"scopes"`
	ServerIDs []string `json:"server_ids"`
	Days      int      `json:"days"`
}

type consentResult struct {
	RedirectTo string `json:"redirect_to"`
}

// Decide records the human's answer.
//
// CSRF is structurally impossible here rather than defended against: the route
// requires an `Authorization` header, which a cross-site form cannot set, and
// the request id is unguessable and single-use.
func (h *MCPOAuthHandlers) Decide(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body consentDecision
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// The id comes from the body here; the query form is only used by the GET.
	if strings.TrimSpace(body.RequestID) != "" {
		q := r.URL.Query()
		q.Set("request", body.RequestID)
		r.URL.RawQuery = q.Encode()
	}
	req, client, ok := h.loadPendingRequest(w, r)
	if !ok {
		return
	}

	if !body.Approve {
		if err := h.store.DenyMCPAuthorization(r.Context(), req.ID); err != nil && !errors.Is(err, store.ErrMCPRequestResolved) {
			writeServerError(w, r, "mcp consent: deny", err)
			return
		}
		audit(h.store, r, "", "mcp.grant.denied", map[string]any{
			"client_id":   client.ClientID,
			"client_name": mcpserver.CleanUntrusted(client.ClientName),
		})
		noStore(w)
		writeJSON(w, http.StatusOK, consentResult{
			RedirectTo: denyRedirect(req.RedirectURI, req.State),
		})
		return
	}

	// What the human approved may be narrower than what the client asked for,
	// but it can never be wider: the approved set is intersected with the parked
	// request before anything is checked or stored.
	scopes, err := store.NormalizeMCPScopes(intersect(body.Scopes, req.Scopes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "select at least one capability")
		return
	}
	if len(body.ServerIDs) == 0 {
		writeError(w, http.StatusBadRequest, "select at least one server")
		return
	}

	// Re-check, right now, that the caller holds every approved scope on every
	// approved server. The consent screen already filtered the list, but that
	// list was built for a different request a moment ago; this is the check
	// that actually decides.
	user, err := h.store.GetUserByID(r.Context(), uid)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	serverIDs := dedupe(body.ServerIDs)
	for _, serverID := range serverIDs {
		if _, err := h.store.GetServer(r.Context(), serverID); err != nil {
			writeError(w, http.StatusBadRequest, "unknown server, or you do not have that access on it")
			return
		}
		for _, scope := range scopes {
			held, err := h.holdsScope(r, user, serverID, scope)
			if err != nil {
				writeServerError(w, r, "mcp consent: permission check", err)
				return
			}
			if !held {
				writeError(w, http.StatusBadRequest, "unknown server, or you do not have that access on it")
				return
			}
		}
	}

	expiresAt, err := grantExpiry(body.Days, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	grant, code, err := h.store.ApproveMCPAuthorization(r.Context(), req.ID, uid, scopes, serverIDs, expiresAt)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMCPRequestResolved):
			writeError(w, http.StatusConflict, "this connection request was already decided")
		case errors.Is(err, store.ErrMCPRequestExpired):
			writeError(w, http.StatusGone, "this connection request expired; start it again from your client")
		default:
			writeServerError(w, r, "mcp consent: approve", err)
		}
		return
	}

	audit(h.store, r, "", "mcp.grant.created", map[string]any{
		"grant_id":    grant.ID,
		"client_id":   client.ClientID,
		"client_name": mcpserver.CleanUntrusted(client.ClientName),
		"scopes":      grant.Scopes,
		"server_ids":  grant.ServerIDs,
		"expires_at":  grant.ExpiresAt,
	})

	// The redirect target is rebuilt from the *stored* redirect URI, never from
	// anything in this request body.
	target, err := url.Parse(req.RedirectURI)
	if err != nil {
		writeServerError(w, r, "mcp consent: redirect", err)
		return
	}
	q := target.Query()
	q.Set("code", code)
	if req.State != "" {
		q.Set("state", req.State)
	}
	target.RawQuery = q.Encode()

	noStore(w)
	writeJSON(w, http.StatusOK, consentResult{RedirectTo: target.String()})
}

// loadPendingRequest resolves the `request` parameter to a parked request that
// is still open. Every failure is a client-visible message with no detail about
// which of the several reasons applied.
func (h *MCPOAuthHandlers) loadPendingRequest(w http.ResponseWriter, r *http.Request) (*store.MCPAuthorizationRequest, *store.MCPClient, bool) {
	id := strings.TrimSpace(r.URL.Query().Get("request"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "this connection request is missing its identifier")
		return nil, nil, false
	}
	req, err := h.store.GetMCPAuthorizationRequest(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "this connection request no longer exists; start it again from your client")
		return nil, nil, false
	}
	if req.ResolvedAt != nil {
		writeError(w, http.StatusConflict, "this connection request was already decided")
		return nil, nil, false
	}
	if !req.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusGone, "this connection request expired; start it again from your client")
		return nil, nil, false
	}
	client, err := h.store.GetMCPClient(r.Context(), req.ClientID)
	if err != nil {
		writeError(w, http.StatusNotFound, "this connection request no longer exists; start it again from your client")
		return nil, nil, false
	}
	return req, client, true
}

// denyRedirect builds the `access_denied` response a client expects when a
// human says no.
func denyRedirect(redirectURI, state string) string {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	q := target.Query()
	q.Set("error", "access_denied")
	q.Set("error_description", "the account holder declined this connection")
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	return target.String()
}

// grantExpiry turns the requested number of days into a bounded expiry.
//
// The bound is checked on `days` rather than on the duration it produces,
// because the multiplication overflows: a large enough day count wraps
// time.Duration around and lands back inside the permitted range, which turned
// an absurd request into a silently short-lived grant instead of the error it
// should have been.
func grantExpiry(days int, now time.Time) (time.Time, error) {
	maxDays := int(store.MaxMCPGrantLifetime / (24 * time.Hour))
	if days > maxDays {
		return time.Time{}, fmt.Errorf("a connection can last at most %d days", maxDays)
	}
	lifetime := store.DefaultMCPGrantLifetime
	if days > 0 {
		lifetime = time.Duration(days) * 24 * time.Hour
	}
	return now.Add(lifetime).UTC(), nil
}

func intersect(requested, allowed []string) []string {
	permitted := make(map[string]bool, len(allowed))
	for _, s := range allowed {
		permitted[s] = true
	}
	out := []string{}
	for _, s := range requested {
		if permitted[strings.TrimSpace(s)] {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ── Token ────────────────────────────────────────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// Token exchanges an authorization code or a refresh token for an access token.
//
// It never accepts a client secret in any form. A public client presenting one
// would be evidence of a misconfiguration, and honoring it would mean treating
// a secret that cannot be kept as though it authenticated anybody.
func (h *MCPOAuthHandlers) Token(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.origin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	noStore(w)

	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		h.tokenFromCode(w, r, origin)
	case "refresh_token":
		h.tokenFromRefresh(w, r, origin)
	default:
		h.tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
	}
}

func (h *MCPOAuthHandlers) tokenFromCode(w http.ResponseWriter, r *http.Request, origin string) {
	code := r.PostFormValue("code")
	verifier := r.PostFormValue("code_verifier")
	clientID := strings.TrimSpace(r.PostFormValue("client_id"))
	redirectURI := strings.TrimSpace(r.PostFormValue("redirect_uri"))

	if code == "" || verifier == "" || clientID == "" || redirectURI == "" {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "the token request is missing required parameters")
		return
	}

	grant, req, err := h.store.RedeemMCPAuthorizationCode(r.Context(), code)
	if err != nil {
		if errors.Is(err, store.ErrMCPCodeReplayed) {
			// The delegation is already revoked by the store; say only that the
			// grant is invalid.
			slog.Warn("mcp: authorization code replayed", "client_id", clientID)
		}
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this authorization code is not usable")
		return
	}

	// Everything below compares against the *parked* request, which was frozen
	// before the human approved it — so a client cannot change the terms of what
	// was approved by sending different values now.
	if req.ClientID != clientID || req.RedirectURI != redirectURI {
		h.revokeAfterTokenAbuse(r, grant, "client or redirect mismatch at token exchange")
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this authorization code is not usable")
		return
	}
	if !auth.VerifyPKCES256(req.CodeChallenge, verifier) {
		h.revokeAfterTokenAbuse(r, grant, "PKCE verification failed")
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this authorization code is not usable")
		return
	}
	// A resource sent at token time must still name this endpoint. Absent is
	// fine: the grant already carries the audience the code was minted for.
	if raw := r.PostFormValue("resource"); raw != "" {
		if _, err := h.canonicalResource(r, raw, origin); err != nil {
			h.tokenError(w, http.StatusBadRequest, "invalid_target", "the resource parameter must name this server's MCP endpoint")
			return
		}
	}
	if grant.Resource != origin+publicurl.MCPResourcePath {
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this authorization code is not usable")
		return
	}

	pair, err := h.store.IssueMCPTokens(r.Context(), grant, grant.Scopes, "")
	if err != nil {
		slog.Error("mcp: issuing tokens failed", "grant_id", grant.ID, "error", err)
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this authorization code is not usable")
		return
	}
	h.writeTokens(w, pair)
}

func (h *MCPOAuthHandlers) tokenFromRefresh(w http.ResponseWriter, r *http.Request, origin string) {
	presented := r.PostFormValue("refresh_token")
	if presented == "" {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	// These clients are public, so client_id is the only thing tying a refresh
	// request to the delegation it claims. RFC 6749 §6 requires it of an
	// unauthenticated client, and the store binds it before consuming anything —
	// checking after rotation would let a wrong client_id burn a live token.
	clientID := strings.TrimSpace(r.PostFormValue("client_id"))
	if clientID == "" {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	_, pair, err := h.store.RotateMCPRefreshToken(r.Context(), presented, clientID, origin+publicurl.MCPResourcePath)
	if err != nil {
		if errors.Is(err, store.ErrMCPRefreshReplayed) {
			slog.Warn("mcp: refresh token replayed; delegation revoked")
		}
		h.tokenError(w, http.StatusBadRequest, "invalid_grant", "this refresh token is not usable")
		return
	}
	h.writeTokens(w, pair)
}

func (h *MCPOAuthHandlers) writeTokens(w http.ResponseWriter, pair *store.MCPTokenPair) {
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  pair.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    pair.ExpiresIn,
		RefreshToken: pair.RefreshToken,
		Scope:        strings.Join(pair.Scopes, " "),
	})
}

// revokeAfterTokenAbuse retires a grant whose code was redeemed with the wrong
// client, redirect, or verifier. The code itself is already consumed, so the
// exchange cannot be retried — but a code that reached the wrong party is
// evidence the delegation is compromised, not merely a failed request.
func (h *MCPOAuthHandlers) revokeAfterTokenAbuse(r *http.Request, grant *store.MCPGrant, reason string) {
	slog.Warn("mcp: revoking grant after token exchange failure", "grant_id", grant.ID, "reason", reason)
	if err := h.store.RevokeMCPGrant(r.Context(), grant.ID, grant.UserID); err != nil {
		slog.Error("mcp: revoking grant failed", "grant_id", grant.ID, "error", err)
	}
	if err := h.store.LogActionWithActor(r.Context(), grant.UserID, "", grant.ID, "",
		"mcp.grant.revoked", clientIP(r), map[string]any{
			"reason":    "token exchange failed verification",
			"client_id": grant.ClientID,
		}); err != nil {
		slog.Error("mcp audit write failed", "action", "mcp.grant.revoked", "error", err)
	}
}

func (h *MCPOAuthHandlers) tokenError(w http.ResponseWriter, status int, code, description string) {
	noStore(w)
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// ── Registration ─────────────────────────────────────────────────

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	SoftwareID              string   `json:"software_id"`
	Scope                   string   `json:"scope"`
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	Scope                   string   `json:"scope"`
}

// Register implements RFC 7591 dynamic client registration for public clients.
//
// This endpoint is open by necessity — a client must be able to register before
// anyone has authenticated — so it is the most exposed surface here. That is
// tolerable only because registering grants nothing: a client id is a name, and
// every capability still requires a human at the consent screen. The validation
// below therefore concentrates on the one field that has security meaning
// later, the redirect URI.
func (h *MCPOAuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.origin(w, r); !ok {
		return
	}
	var body registrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)).Decode(&body); err != nil {
		h.tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "the registration body could not be read")
		return
	}

	// A client that claims it can authenticate is one that thinks it can keep a
	// secret. Rejecting it is more honest than silently downgrading it to a
	// public client it did not intend to be.
	if m := strings.TrimSpace(body.TokenEndpointAuthMethod); m != "" && m != "none" {
		h.tokenError(w, http.StatusBadRequest, "invalid_client_metadata",
			"this server registers public clients only; token_endpoint_auth_method must be none")
		return
	}
	for _, gt := range body.GrantTypes {
		if gt != "authorization_code" && gt != "refresh_token" {
			h.tokenError(w, http.StatusBadRequest, "invalid_client_metadata",
				"only the authorization_code and refresh_token grant types are supported")
			return
		}
	}
	for _, rt := range body.ResponseTypes {
		if rt != "code" {
			h.tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "only the code response type is supported")
			return
		}
	}
	if body.Scope != "" {
		if _, err := store.NormalizeMCPScopes(strings.Fields(body.Scope)); err != nil {
			h.tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "one of the requested capabilities is not supported")
			return
		}
	}

	uris, err := validateRedirectURIs(body.RedirectURIs, h.allowRemoteRedirects)
	if err != nil {
		h.tokenError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}

	// A self-registered client chooses its own name, and that name is later read
	// by a human deciding whether to approve it. Bidi overrides and zero-width
	// characters are invisible to that human and perfectly legible to everything
	// else, so they are stripped here rather than only where the name is
	// rendered — a name registered clean cannot be forgotten on a later surface.
	//
	// Length is left to the store, which caps both fields on rune boundaries.
	name := mcpserver.CleanUntrusted(body.ClientName)
	softwareID := mcpserver.CleanUntrusted(body.SoftwareID)

	client, err := h.store.RegisterMCPClient(r.Context(), name, uris, "dynamic", softwareID)
	if err != nil {
		if errors.Is(err, store.ErrMCPRedirectRequired) {
			h.tokenError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		slog.Error("mcp: client registration failed", "error", err)
		h.tokenError(w, http.StatusInternalServerError, "server_error", "registration could not be completed")
		return
	}

	noStore(w)
	writeJSON(w, http.StatusCreated, registrationResponse{
		ClientID:                client.ClientID,
		ClientName:              client.ClientName,
		RedirectURIs:            client.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
		Scope:                   strings.Join(store.AllMCPScopes(), " "),
	})
}

// validateRedirectURIs enforces what a redirect URI may be. This is the field
// an authorization code is eventually delivered to, so every relaxation here is
// a way to deliver one to the wrong place.
//
// Loopback `http` is allowed because that is how a CLI on the operator's own
// machine receives its callback, and the traffic never leaves the host. A
// wildcard, a fragment, or embedded credentials are refused outright, and
// matching later is exact — so there is no normalization step for an attacker
// to exploit the difference across.
//
// allowRemote decides whether the host may be somewhere other than this
// machine, and it is false for dynamic registration. That endpoint is open by
// necessity, so without the restriction anyone could register a client whose
// callback is a server they control, hand the resulting /authorize link to an
// operator, and collect the code the moment that operator approved it. The
// consent screen warns about exactly this — it flags a self-registered client
// as a name-claim and shows the callback host — but a warning is a request that
// the human notice, and refusing the address outright is not. Every client this
// feature targets (Claude Code, Codex, Hermes) calls back to loopback, so the
// restriction costs them nothing; a hosted client needs an administrator to
// pre-register it, or MCP_ALLOW_REMOTE_REDIRECTS=true to accept the trade.
func validateRedirectURIs(raw []string, allowRemote bool) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("at least one redirect URI is required")
	}
	if len(raw) > maxRedirectURIs {
		return nil, errors.New("too many redirect URIs")
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || len(candidate) > maxRedirectURILen {
			return nil, errors.New("a redirect URI is empty or too long")
		}
		if strings.Contains(candidate, "*") {
			return nil, errors.New("redirect URIs must not contain wildcards")
		}
		u, err := url.Parse(candidate)
		if err != nil || !u.IsAbs() {
			return nil, errors.New("redirect URIs must be absolute URLs")
		}
		if u.Fragment != "" || strings.Contains(candidate, "#") {
			return nil, errors.New("redirect URIs must not contain a fragment")
		}
		if u.User != nil {
			return nil, errors.New("redirect URIs must not embed credentials")
		}
		switch u.Scheme {
		case "https":
		case "http":
			if !publicurl.IsLoopbackHost(u.Host) {
				return nil, errors.New("an http redirect URI is only allowed on loopback")
			}
		default:
			return nil, errors.New("redirect URIs must use http (loopback only) or https")
		}
		if u.Host == "" {
			return nil, errors.New("redirect URIs must include a host")
		}
		if !allowRemote && !publicurl.IsLoopbackHost(u.Host) {
			return nil, errors.New(
				"a self-registered client must call back to this machine (127.0.0.1, [::1], or localhost); " +
					"ask an administrator to pre-register a client that redirects elsewhere")
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out, nil
}

// ── Revocation ───────────────────────────────────────────────────

// Revoke implements RFC 7009. It always answers 200 for a well-formed request,
// whether or not the token existed or belonged to the named client: reporting
// the difference would turn this endpoint into an oracle for guessing valid
// tokens.
//
// client_id is required. A public client authenticates to the revocation
// endpoint by naming itself (RFC 7009 §2.1), and the store only revokes a token
// issued to that client — otherwise anyone holding a refresh token could retire
// a delegation they do not own. Its absence is a malformed request rather than
// a silent success, so a client that omits it learns its tokens are still live.
func (h *MCPOAuthHandlers) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	clientID := strings.TrimSpace(r.PostFormValue("client_id"))
	if clientID == "" {
		h.tokenError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	if token := r.PostFormValue("token"); token != "" {
		if err := h.store.RevokeMCPTokenByValue(r.Context(), token, clientID); err != nil {
			slog.Error("mcp: revocation failed", "error", err)
		}
	}
	noStore(w)
	w.WriteHeader(http.StatusOK)
}

// noStore marks a response as uncacheable. Applied to everything that carries
// token material or a decision about one.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
