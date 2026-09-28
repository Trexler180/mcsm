package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ── Remote MCP delegation ────────────────────────────────────────
//
// An OAuth 2.1 delegation model for AI agents the operator already runs. It is
// deliberately a separate credential family from access keys: a key is a secret
// its owner carries, a grant is a delegation its owner made in a browser, and
// keeping the two apart means neither can be presented where the other is
// expected.
//
// What they share is the authorization vocabulary. A grant's scopes and server
// allowlist are only ever bounds; the effective authority behind any tool call
// is
//
//	active grant ∩ unexpired audience-bound token ∩ server allowlist
//	             ∩ MCP scope ∩ the owner's live per-server RBAC
//
// and every term is re-read per call. Nothing here is cached, because immediate
// revocation is a required invariant and a cache is exactly the window in which
// a revoked grant still works.

const (
	// Reserved bearer prefixes, all distinct from auth.AccessKeyPrefix so the
	// two credential families can never be confused at a boundary or in a log.
	MCPCodePrefix    = "mcsm_mcpc_"
	MCPAccessPrefix  = "mcsm_mcpa_"
	MCPRefreshPrefix = "mcsm_mcpr_"

	// MCPCodeLifetime is deliberately tiny: a code is handed straight from the
	// browser redirect to the client's loopback listener and redeemed at once.
	MCPCodeLifetime = 60 * time.Second

	// MCPAccessTokenLifetime matches the browser JWT's lifetime. Short enough
	// that a leaked token is a small window, long enough that a diagnosis
	// session isn't a refresh storm.
	MCPAccessTokenLifetime = 15 * time.Minute

	// MCPRefreshTokenLifetime bounds a refresh chain independently of the
	// grant. A refresh token never outlives its grant regardless.
	MCPRefreshTokenLifetime = 30 * 24 * time.Hour

	// MCPRefreshReuseGrace is how long after rotation a refresh token may be
	// presented again without that counting as theft. A client whose token
	// response was lost in transit retries with the token it still holds, and
	// two sessions sharing one stored credential refresh at the same moment;
	// neither is a leak, and revoking the delegation for them disconnects the
	// operator for a network blip. Outside this window a second presentation is
	// still a replay and still retires the grant.
	MCPRefreshReuseGrace = 30 * time.Second

	// MCPAuthorizationRequestLifetime is how long a parked /authorize call
	// waits for a human decision — long enough to sign in and read the screen.
	MCPAuthorizationRequestLifetime = 10 * time.Minute

	// Grant lifetime bounds. Default short, maximum the same 90 days access
	// keys allow, so one policy statement covers both families.
	DefaultMCPGrantLifetime = 30 * 24 * time.Hour
	MaxMCPGrantLifetime     = 90 * 24 * time.Hour

	// mcpTokenBytes is the entropy behind every code and token (256 bits).
	mcpTokenBytes = 32

	// mcpGrantUsageStale mirrors the access-key rule: a polling agent must not
	// cost one row write per tool call.
	mcpGrantUsageStale = 5 * time.Minute

	// maxMCPClientNameRunes bounds the display text a self-registering client
	// picks for itself. Counted in runes, not bytes: see truncateRunes.
	maxMCPClientNameRunes = 100
)

// truncateRunes cuts a string to a rune budget.
//
// Slicing by byte is what this replaces, and it is wrong for any field a
// stranger fills in: cutting mid-sequence stores invalid UTF-8, which reads
// back as a replacement character and is the kind of value that behaves
// differently in a database, a log, and a browser. Every MCP field bounded
// here is attacker-chosen, so none of them may be cut that way.
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

var (
	// ErrMCPTokenInvalid is the single uniform failure for every unusable
	// token state — unknown, expired, revoked, wrong audience, orphaned, or
	// malformed. Callers must not let a client tell them apart.
	ErrMCPTokenInvalid = errors.New("invalid mcp token")

	// ErrMCPCodeReplayed means a consumed authorization code was presented
	// again. That can only be theft or a broken client, and either way the
	// grant behind it is no longer trustworthy.
	ErrMCPCodeReplayed = errors.New("authorization code already used")

	// ErrMCPRefreshReplayed means a rotated refresh token was presented again
	// after MCPRefreshReuseGrace. The whole grant is revoked when this happens.
	ErrMCPRefreshReplayed = errors.New("refresh token already used")

	// errMCPRefreshLostRace reports that a concurrent presentation consumed the
	// refresh token first. RotateMCPRefreshToken settles it as a reuse.
	errMCPRefreshLostRace = errors.New("refresh token consumed concurrently")

	ErrMCPClientNotFound   = errors.New("mcp client not found")
	ErrMCPRequestNotFound  = errors.New("authorization request not found")
	ErrMCPRequestResolved  = errors.New("authorization request already resolved")
	ErrMCPRequestExpired   = errors.New("authorization request expired")
	ErrMCPGrantNotFound    = errors.New("mcp grant not found")
	ErrMCPNoScopes         = errors.New("at least one capability is required")
	ErrMCPNoServers        = errors.New("at least one server is required")
	ErrMCPUnknownScope     = errors.New("unknown capability")
	ErrMCPRedirectRequired = errors.New("at least one redirect URI is required")
	ErrMCPRedirectInvalid  = errors.New("redirect URI must be an exact https URL, or http on loopback")
)

// ── Scope vocabulary ─────────────────────────────────────────────

// MCPScope is an OAuth scope this authorization server issues. The vocabulary
// is closed and curated rather than mirroring the full permission lattice: a
// scope here names a capability of the *tool facade*, and every tool facade
// capability must map onto a permission the owner already holds.
type MCPScope string

const (
	MCPScopeServersRead     MCPScope = "mcp:servers.read"
	MCPScopeDiagnosticsRead MCPScope = "mcp:diagnostics.read"
	MCPScopeLogsRead        MCPScope = "mcp:logs.read"
	MCPScopeMetricsRead     MCPScope = "mcp:metrics.read"
	MCPScopeAuditRead       MCPScope = "mcp:audit.read"
	MCPScopeActionsRequest  MCPScope = "mcp:actions.request"
	MCPScopePowerStart      MCPScope = "mcp:power.start"
	MCPScopePowerStop       MCPScope = "mcp:power.stop"
	MCPScopePowerRestart    MCPScope = "mcp:power.restart"
	MCPScopeModsRead        MCPScope = "mcp:mods.read"
	MCPScopeModsInstall     MCPScope = "mcp:mods.install"
	MCPScopeModsUpdate      MCPScope = "mcp:mods.update"
	MCPScopeModsRemove      MCPScope = "mcp:mods.remove"
	MCPScopeBackupsCreate   MCPScope = "mcp:backups.create"

	// MCPScopePlayersWhitelist covers adding and removing one named player from
	// the whitelist. It is deliberately its own scope rather than part of a
	// broader "players" capability: whitelisting is the one player write that is
	// fully reversible and does not confer authority in-game, so it can be
	// delegated without also delegating op, ban, or kick.
	MCPScopePlayersWhitelist MCPScope = "mcp:players.whitelist"

	// MCPScopeLogsRaw covers reading the raw text of two fixed files: the current
	// console log and the newest crash report.
	//
	// It is separate from mcp:logs.read, which consents to ServerManager's
	// indexed warnings — a curated, already-bounded view. Raw output is a
	// different category: it is whatever the server wrote, it is the natural
	// home of a pasted credential, and a human ticking "read recent log events"
	// is not agreeing to it.
	//
	// It is deliberately *not* named for files. This facade has no file
	// capability and must never appear to: no tool under this scope accepts a
	// path, and the two it can reach are constants in mcpserver. The naming
	// guard in mcp_operator_scopes_test.go exists to keep that true, and the
	// precedent it records is console.run — a forbidden name became honest only
	// once the thing behind it was a closed set.
	MCPScopeLogsRaw MCPScope = "mcp:logs.raw"

	// MCPScopeConfigRead covers reading one file: the server's own
	// server.properties, redacted. That is where difficulty, whitelist
	// enforcement and the MOTD live — settings ServerManager does not hold in
	// its own record. It reads that file and no other, and writes nothing.
	MCPScopeConfigRead MCPScope = "mcp:config.read"

	// MCPScopePlayersRead covers seeing who plays on a server: the whitelist,
	// the ban list, and recently seen players. It is separate from
	// mcp:players.whitelist because reading the roster and changing it are
	// different decisions, and because this is the one scope that hands an
	// agent people's identities rather than the server's own state.
	MCPScopePlayersRead MCPScope = "mcp:players.read"

	// MCPScopeConsoleRun covers the fixed, allowlisted console verbs in
	// mcpserver's console vocabulary. It is NOT arbitrary console access: the
	// facade never forwards a model-authored command string, and each verb is
	// additionally gated on the permission that verb's effect requires.
	MCPScopeConsoleRun MCPScope = "mcp:console.run"
)

// mcpScopeConsent maps each scope to the permission its holder must currently
// have on every selected server. The `group` flag has the same meaning as in
// the HTTP route gates: a group requirement is satisfied by the group or any
// leaf beneath it.
//
// `mcp:actions.request` maps to group access on `power` because the specific
// leaf depends on the action being requested — that is checked again, exactly,
// when the request is filed and once more when a human approves it.
var mcpScopeConsent = map[MCPScope]struct {
	permission ServerPermission
	group      bool
}{
	MCPScopeServersRead:     {ServerPermissionView, false},
	MCPScopeDiagnosticsRead: {ServerPermissionView, false},
	MCPScopeLogsRead:        {ServerPermissionView, false},
	MCPScopeMetricsRead:     {ServerPermissionView, false},
	MCPScopeAuditRead:       {ServerPermissionView, false},
	MCPScopeActionsRequest:  {ServerPermissionPower, true},
	MCPScopePowerStart:      {ServerPermissionPowerStart, false},
	MCPScopePowerStop:       {ServerPermissionPowerStop, false},
	MCPScopePowerRestart:    {ServerPermissionPowerRestart, false},
	MCPScopeModsRead:        {ServerPermissionMods, true},
	MCPScopeModsInstall:     {ServerPermissionModsInstall, false},
	MCPScopeModsUpdate:      {ServerPermissionModsUpdate, false},
	MCPScopeModsRemove:      {ServerPermissionModsRemove, false},
	MCPScopeBackupsCreate:   {ServerPermissionBackupsCreate, false},

	MCPScopePlayersWhitelist: {ServerPermissionPlayersWhitelist, false},

	// Both new read scopes map to the permission that already governs the same
	// data on the HTTP routes, so a delegation can never see through this facade
	// what its owner cannot see in the dashboard.
	MCPScopeLogsRaw:     {ServerPermissionFilesRead, false},
	MCPScopeConfigRead:  {ServerPermissionFilesRead, false},
	MCPScopePlayersRead: {ServerPermissionPlayersInspect, false},

	// `mcp:console.run` consents to the console capability as a whole, because
	// that is the permission a human reading the screen understands. It is the
	// floor, not the whole check: a verb whose effect needs a narrower leaf (kick
	// needs players.kick, whitelist list/reload needs players.whitelist) is checked
	// against that leaf too, so this scope can never widen what its holder may
	// already do.
	MCPScopeConsoleRun: {ServerPermissionConsole, false},
}

// AllMCPScopes returns the supported scope vocabulary in a stable order. It is
// what the authorization-server metadata advertises and what the consent screen
// offers.
func AllMCPScopes() []string {
	return []string{
		string(MCPScopeServersRead),
		string(MCPScopeDiagnosticsRead),
		string(MCPScopeLogsRead),
		string(MCPScopeMetricsRead),
		string(MCPScopeAuditRead),
		string(MCPScopeActionsRequest),
		string(MCPScopePowerStart),
		string(MCPScopePowerStop),
		string(MCPScopePowerRestart),
		string(MCPScopeModsRead),
		string(MCPScopeModsInstall),
		string(MCPScopeModsUpdate),
		string(MCPScopeModsRemove),
		string(MCPScopeBackupsCreate),
		string(MCPScopePlayersWhitelist),
		string(MCPScopePlayersRead),
		string(MCPScopeLogsRaw),
		string(MCPScopeConfigRead),
		string(MCPScopeConsoleRun),
	}
}

// MCPScopeRequirement reports the live permission a scope needs on every
// granted server, and whether group access satisfies it. ok is false for an
// unknown scope, which must always be rejected rather than ignored.
func MCPScopeRequirement(scope string) (permission ServerPermission, group bool, ok bool) {
	req, found := mcpScopeConsent[MCPScope(scope)]
	if !found {
		return "", false, false
	}
	return req.permission, req.group, true
}

// MCPActionPermission maps a requestable action onto the exact power leaf it
// needs. Only start, stop, and restart are requestable in this phase — kill,
// reinstall, restore, and everything else is deliberately unreachable.
func MCPActionPermission(action string) (ServerPermission, bool) {
	if _, ok := ParseMCPUpgradeAction(action); ok {
		return ServerPermissionSettings, true
	}
	switch action {
	case "start":
		return ServerPermissionPowerStart, true
	case "stop":
		return ServerPermissionPowerStop, true
	case "restart":
		return ServerPermissionPowerRestart, true
	}
	return "", false
}

func MCPUpgradeAction(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" || len(target) > 64 {
		return "", false
	}
	for _, r := range target {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_') {
			return "", false
		}
	}
	return "upgrade:" + target, true
}

func ParseMCPUpgradeAction(action string) (string, bool) {
	if !strings.HasPrefix(action, "upgrade:") {
		return "", false
	}
	target := strings.TrimPrefix(action, "upgrade:")
	encoded, ok := MCPUpgradeAction(target)
	return target, ok && encoded == action
}

// NormalizeMCPScopes validates, de-dupes, and sorts a requested scope set
// against the closed vocabulary. An unknown scope is an error, never a silent
// drop: a client asking for something we don't understand must be told so
// rather than quietly given less than it thinks it has.
func NormalizeMCPScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, _, ok := MCPScopeRequirement(s); !ok {
			return nil, fmt.Errorf("%w: %s", ErrMCPUnknownScope, s)
		}
		seen[s] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, ErrMCPNoScopes
	}
	return out, nil
}

// HasMCPScope reports whether a granted scope set carries a capability. There
// is no hierarchy and no wildcard: a scope is held or it is not.
func HasMCPScope(scopes []string, want MCPScope) bool {
	for _, s := range scopes {
		if s == string(want) {
			return true
		}
	}
	return false
}

// ── Models ───────────────────────────────────────────────────────

// MCPClient is a registered public OAuth client. There is no secret field:
// Claude Code and Codex run on the operator's machine and cannot keep one, and
// issuing a pretend secret would invite treating them as confidential.
type MCPClient struct {
	ClientID     string    `json:"client_id"`
	ClientName   string    `json:"client_name"`
	RedirectURIs []string  `json:"redirect_uris"`
	Origin       string    `json:"origin"`
	SoftwareID   *string   `json:"software_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// AllowsRedirect reports whether a redirect URI matches one the client
// registered, compared as an exact string. No normalization, no prefix match,
// no wildcard — that comparison is where redirect bypasses live.
func (c *MCPClient) AllowsRedirect(uri string) bool {
	if c == nil {
		return false
	}
	for _, u := range c.RedirectURIs {
		if u == uri {
			return true
		}
	}
	return false
}

// MCPAuthorizationRequest is one /authorize call parked while a human decides.
// Everything the consent screen shows and the eventual code needs is frozen
// here, so nothing the client sends later can change what was approved.
type MCPAuthorizationRequest struct {
	ID                  string
	ClientID            string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Scopes              []string
	Resource            string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	ResolvedAt          *time.Time
}

// MCPGrant is the durable delegation: this user let this client reach these
// servers with these capabilities until this date.
type MCPGrant struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	Scopes     []string   `json:"scopes"`
	ServerIDs  []string   `json:"server_ids"`
	Resource   string     `json:"resource"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastUsedIP *string    `json:"last_used_ip"`

	// Approval policy overrides. nil means "inherit the owner's account
	// default" — a real third state, distinct from an explicit false, so a
	// grant never silently pins whatever the default happened to be when it
	// was made. Resolve them through ResolveApprovalPolicy rather than reading
	// them directly, so the precedence rule lives in exactly one place.
	RequirePassword      *bool `json:"require_password"`
	AutoApproveLifecycle *bool `json:"auto_approve_lifecycle"`
	AutoApproveUpgrades  *bool `json:"auto_approve_upgrades"`
}

// Active reports whether the grant would authorize anything right now.
func (g *MCPGrant) Active(now time.Time) bool {
	if g == nil || g.RevokedAt != nil {
		return false
	}
	return g.ExpiresAt.After(now) && len(g.Scopes) > 0 && len(g.ServerIDs) > 0
}

// AllowsServer reports whether the grant's allowlist covers a server. An empty
// allowlist allows nothing — a grant with no servers is inert by construction.
func (g *MCPGrant) AllowsServer(serverID string) bool {
	if g == nil || serverID == "" {
		return false
	}
	for _, id := range g.ServerIDs {
		if id == serverID {
			return true
		}
	}
	return false
}

// MCPPrincipal is what a valid access token resolves to: the grant's bounds,
// the live owner row behind it, and the token's own expiry. Like the machine
// principal, none of it grants access on its own.
type MCPPrincipal struct {
	Grant *MCPGrant
	User  *User
	// Scopes are the token's scopes, which are always a subset of the grant's.
	Scopes         []string
	TokenExpiresAt time.Time
}

// ── Token material ───────────────────────────────────────────────

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashMCPToken hashes a presented code or token. These are high-entropy random
// strings, so a fast SHA-256 is right — the same reasoning that applies to
// refresh tokens and access keys, and not to human passwords.
func HashMCPToken(token string) string { return sha256Hex(token) }

// generateMCPToken mints one credential: the raw string (which exists only in
// the response that carries it) and the hash that is all the database ever
// sees.
func generateMCPToken(prefix string) (token, hash string, err error) {
	b := make([]byte, mcpTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("mcp token rand: %w", err)
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashMCPToken(token), nil
}

// ── Clients ──────────────────────────────────────────────────────

// RegisterMCPClient records a public client. Redirect-URI validation happens in
// the handler (which knows whether the deployment is loopback); this stores
// what was accepted and returns the client id the client will present.
func (s *Store) RegisterMCPClient(ctx context.Context, name string, redirectURIs []string, origin, softwareID string) (*MCPClient, error) {
	name = truncateRunes(strings.TrimSpace(name), maxMCPClientNameRunes)
	if name == "" {
		name = "Unnamed MCP client"
	}
	softwareID = truncateRunes(strings.TrimSpace(softwareID), maxMCPClientNameRunes)
	if len(redirectURIs) == 0 {
		return nil, ErrMCPRedirectRequired
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_clients (client_id, client_name, redirect_uris, origin, software_id)
		 VALUES (?,?,?,?,?)`,
		id, name, strArray(redirectURIs), origin, nullIfEmpty(softwareID),
	); err != nil {
		return nil, err
	}
	return s.GetMCPClient(ctx, id)
}

func (s *Store) GetMCPClient(ctx context.Context, clientID string) (*MCPClient, error) {
	var c MCPClient
	var uris strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, client_name, redirect_uris, origin, software_id, created_at
		   FROM mcp_clients WHERE client_id = ?`, clientID,
	).Scan(&c.ClientID, &c.ClientName, &uris, &c.Origin, &c.SoftwareID, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPClientNotFound
	}
	if err != nil {
		return nil, err
	}
	c.RedirectURIs = []string(uris)
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	return &c, nil
}

// ── Authorization requests ───────────────────────────────────────

// CreateMCPAuthorizationRequest parks a validated /authorize call for a human
// decision and returns its unguessable id, which is the only handle the consent
// screen ever uses.
func (s *Store) CreateMCPAuthorizationRequest(ctx context.Context, req *MCPAuthorizationRequest) (*MCPAuthorizationRequest, error) {
	req.ID = uuid.NewString()
	req.CreatedAt = time.Now().UTC()
	req.ExpiresAt = req.CreatedAt.Add(MCPAuthorizationRequestLifetime)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_authorization_requests
		   (id, client_id, redirect_uri, state, code_challenge, code_challenge_method, scopes, resource, created_at, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		req.ID, req.ClientID, req.RedirectURI, nullIfEmpty(req.State), req.CodeChallenge,
		req.CodeChallengeMethod, strArray(req.Scopes), req.Resource, req.CreatedAt, req.ExpiresAt,
	); err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Store) GetMCPAuthorizationRequest(ctx context.Context, id string) (*MCPAuthorizationRequest, error) {
	var r MCPAuthorizationRequest
	var scopes strArray
	var state *string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, client_id, redirect_uri, state, code_challenge, code_challenge_method,
		        scopes, resource, created_at, expires_at, resolved_at
		   FROM mcp_authorization_requests WHERE id = ?`, id,
	).Scan(&r.ID, &r.ClientID, &r.RedirectURI, &state, &r.CodeChallenge, &r.CodeChallengeMethod,
		&scopes, &r.Resource, &r.CreatedAt, &r.ExpiresAt, &r.ResolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPRequestNotFound
	}
	if err != nil {
		return nil, err
	}
	if state != nil {
		r.State = *state
	}
	r.Scopes = []string(scopes)
	return &r, nil
}

// ── Grants and codes ─────────────────────────────────────────────

const mcpGrantColumns = `id, user_id, client_id, client_name, scopes, server_ids, resource,
	 created_at, expires_at, revoked_at, last_used_at, last_used_ip,
	 require_password, auto_approve_lifecycle, auto_approve_upgrades`

func scanMCPGrant(scan func(dest ...any) error) (*MCPGrant, error) {
	var g MCPGrant
	var scopes, servers strArray
	// The three policy overrides are nullable in SQLite and tri-state in Go, so
	// they scan through *int64 rather than *bool: NULL stays nil (inherit),
	// while 0 and 1 become an explicit false/true.
	var reqPw, autoLife, autoUp *int64
	if err := scan(&g.ID, &g.UserID, &g.ClientID, &g.ClientName, &scopes, &servers, &g.Resource,
		&g.CreatedAt, &g.ExpiresAt, &g.RevokedAt, &g.LastUsedAt, &g.LastUsedIP,
		&reqPw, &autoLife, &autoUp); err != nil {
		return nil, err
	}
	g.RequirePassword = nullableBool(reqPw)
	g.AutoApproveLifecycle = nullableBool(autoLife)
	g.AutoApproveUpgrades = nullableBool(autoUp)
	g.Scopes = []string(scopes)
	g.ServerIDs = []string(servers)
	if g.Scopes == nil {
		g.Scopes = []string{}
	}
	if g.ServerIDs == nil {
		g.ServerIDs = []string{}
	}
	return &g, nil
}

// ApproveMCPAuthorization turns a parked request into a durable grant plus a
// single-use code, in one transaction. The request is resolved with a
// conditional UPDATE, so two simultaneous approvals cannot both mint a grant.
//
// scopes and serverIDs are what the *human* approved, which may be narrower
// than what the client asked for. Authorization of those bounds against live
// RBAC belongs to the handler, which knows the request context.
func (s *Store) ApproveMCPAuthorization(ctx context.Context, requestID, userID string, scopes, serverIDs []string, expiresAt time.Time) (*MCPGrant, string, error) {
	req, err := s.GetMCPAuthorizationRequest(ctx, requestID)
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	if req.ResolvedAt != nil {
		return nil, "", ErrMCPRequestResolved
	}
	if !req.ExpiresAt.After(now) {
		return nil, "", ErrMCPRequestExpired
	}
	if len(scopes) == 0 {
		return nil, "", ErrMCPNoScopes
	}
	if len(serverIDs) == 0 {
		return nil, "", ErrMCPNoServers
	}
	client, err := s.GetMCPClient(ctx, req.ClientID)
	if err != nil {
		return nil, "", err
	}

	code, codeHash, err := generateMCPToken(MCPCodePrefix)
	if err != nil {
		return nil, "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	// Claim the request. Losing this race means someone else already decided.
	res, err := tx.ExecContext(ctx,
		`UPDATE mcp_authorization_requests SET resolved_at = ? WHERE id = ? AND resolved_at IS NULL`,
		now, requestID)
	if err != nil {
		return nil, "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, "", ErrMCPRequestResolved
	}

	grantID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO mcp_grants (id, user_id, client_id, client_name, scopes, server_ids, resource, created_at, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		grantID, userID, req.ClientID, client.ClientName, strArray(scopes), strArray(serverIDs),
		req.Resource, now, expiresAt.UTC(),
	); err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO mcp_authorization_codes (code_hash, request_id, grant_id, created_at, expires_at)
		 VALUES (?,?,?,?,?)`,
		codeHash, requestID, grantID, now, now.Add(MCPCodeLifetime),
	); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}

	grant, err := s.getMCPGrant(ctx, grantID)
	if err != nil {
		return nil, "", err
	}
	return grant, code, nil
}

// DenyMCPAuthorization resolves a parked request without creating anything.
func (s *Store) DenyMCPAuthorization(ctx context.Context, requestID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE mcp_authorization_requests SET resolved_at = ? WHERE id = ? AND resolved_at IS NULL`,
		time.Now().UTC(), requestID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMCPRequestResolved
	}
	return nil
}

func (s *Store) getMCPGrant(ctx context.Context, id string) (*MCPGrant, error) {
	g, err := scanMCPGrant(s.db.QueryRowContext(ctx,
		`SELECT `+mcpGrantColumns+` FROM mcp_grants WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPGrantNotFound
	}
	return g, err
}

// ListMCPGrants returns a user's delegations, newest first, including revoked
// and expired ones so the owner keeps a record of what existed.
func (s *Store) ListMCPGrants(ctx context.Context, userID string) ([]*MCPGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mcpGrantColumns+` FROM mcp_grants WHERE user_id = ? ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MCPGrant{}
	for rows.Next() {
		g, err := scanMCPGrant(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetMCPGrantForUser reads one grant scoped to its owner — a grant id belonging
// to someone else is indistinguishable from one that does not exist.
func (s *Store) GetMCPGrantForUser(ctx context.Context, id, userID string) (*MCPGrant, error) {
	g, err := scanMCPGrant(s.db.QueryRowContext(ctx,
		`SELECT `+mcpGrantColumns+` FROM mcp_grants WHERE id = ? AND user_id = ?`, id, userID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPGrantNotFound
	}
	return g, err
}

// RevokeMCPGrant is the one act that stops everything beneath a delegation: the
// grant, every access token, and every refresh family under it. Idempotent, and
// the row survives so audit history keeps naming a real delegation.
func (s *Store) RevokeMCPGrant(ctx context.Context, id, userID string) error {
	if _, err := s.GetMCPGrantForUser(ctx, id, userID); err != nil {
		return err
	}
	return s.revokeMCPGrantByID(ctx, id)
}

func (s *Store) revokeMCPGrantByID(ctx context.Context, id string) error {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE mcp_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE grant_id = ? AND revoked_at IS NULL`, now, id); err != nil {
		return err
	}
	// Any code not yet redeemed is dead too; consuming it later must not
	// resurrect the delegation.
	if _, err := tx.ExecContext(ctx,
		`UPDATE mcp_authorization_codes SET consumed_at = ? WHERE grant_id = ? AND consumed_at IS NULL`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RedeemMCPAuthorizationCode consumes a code exactly once and returns the grant
// and the request it was issued against (whose PKCE challenge and redirect URI
// the caller must still verify).
//
// Presenting a code that was *already* consumed can only be theft or a badly
// broken client. Either way the delegation is no longer trustworthy, so that
// presentation revokes the whole grant rather than merely failing.
//
// Two presentations racing each other are a different thing and are treated as
// one. A client double-submitting its own token exchange must not destroy the
// delegation it is in the middle of establishing, and revoking there would take
// the winner down with it: the winner re-reads the grant below and would find
// it dead, so neither caller would end up with a usable token.
//
// The two are told apart by `redeemed_at`, which the winner sets on its way
// out. A presentation that finds the code claimed but not yet redeemed
// overlapped an exchange in flight; one that finds it redeemed arrived after
// that exchange finished, which is what a replay is. It is a recorded fact
// rather than a clock comparison because a whole redemption can complete inside
// a single tick of the system clock.
//
// A thief who wins the race is still stopped one layer up. Whoever redeems the
// code must then prove possession of the PKCE verifier, and the token endpoint
// revokes the grant when that fails.
func (s *Store) RedeemMCPAuthorizationCode(ctx context.Context, presented string) (*MCPGrant, *MCPAuthorizationRequest, error) {
	if !strings.HasPrefix(presented, MCPCodePrefix) || len(presented) <= len(MCPCodePrefix) {
		return nil, nil, ErrMCPTokenInvalid
	}
	hash := HashMCPToken(presented)

	var requestID, grantID string
	var expiresAt time.Time
	var consumedAt *time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT request_id, grant_id, expires_at, consumed_at FROM mcp_authorization_codes WHERE code_hash = ?`, hash,
	).Scan(&requestID, &grantID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrMCPTokenInvalid
	}
	if err != nil {
		slog.Warn("mcp code lookup failed", "error", err)
		return nil, nil, ErrMCPTokenInvalid
	}
	if consumedAt != nil {
		return nil, nil, s.classifyClaimedCode(ctx, hash, grantID)
	}
	if !expiresAt.After(time.Now()) {
		return nil, nil, ErrMCPTokenInvalid
	}

	// The conditional UPDATE is the actual single-use guarantee; the read above
	// only decides which error to report.
	res, err := s.db.ExecContext(ctx,
		`UPDATE mcp_authorization_codes SET consumed_at = ? WHERE code_hash = ? AND consumed_at IS NULL`,
		time.Now().UTC(), hash)
	if err != nil {
		return nil, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Someone claimed it between our read and our update. Same question,
		// same answer: it depends on whether their exchange has finished.
		return nil, nil, s.classifyClaimedCode(ctx, hash, grantID)
	}

	grant, err := s.getMCPGrant(ctx, grantID)
	if err != nil {
		return nil, nil, err
	}
	if !grant.Active(time.Now()) {
		return nil, nil, ErrMCPTokenInvalid
	}
	req, err := s.GetMCPAuthorizationRequest(ctx, requestID)
	if err != nil {
		return nil, nil, err
	}
	// The exchange is done. From here a further presentation of this code is a
	// replay rather than a duplicate of something still running, so mark it —
	// last, so a redemption that failed above never looks finished.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mcp_authorization_codes SET redeemed_at = ? WHERE code_hash = ? AND redeemed_at IS NULL`,
		time.Now().UTC(), hash); err != nil {
		slog.Error("marking authorization code redeemed failed", "grant_id", grantID, "error", err)
	}
	return grant, req, nil
}

// classifyClaimedCode decides what an already-claimed authorization code means
// for the caller that just presented it, and acts on that reading.
//
// A code claimed but not yet redeemed belongs to an exchange still in flight,
// so this presentation is a duplicate of it — most often a client submitting
// its token request twice. That fails, but quietly: the caller that won is
// about to need this grant, and revoking here would leave neither of them with
// a usable token.
//
// A code already redeemed means the exchange finished before this presentation
// arrived, which is what theft looks like. The delegation is retired.
func (s *Store) classifyClaimedCode(ctx context.Context, hash, grantID string) error {
	var redeemedAt *time.Time
	if err := s.db.QueryRowContext(ctx,
		`SELECT redeemed_at FROM mcp_authorization_codes WHERE code_hash = ?`, hash).Scan(&redeemedAt); err != nil {
		return ErrMCPTokenInvalid
	}
	if redeemedAt == nil {
		slog.Warn("mcp: concurrent presentations of one authorization code", "grant_id", grantID)
		return ErrMCPTokenInvalid
	}
	if err := s.revokeMCPGrantByID(ctx, grantID); err != nil {
		slog.Error("revoking grant after code replay failed", "grant_id", grantID, "error", err)
	}
	return ErrMCPCodeReplayed
}

// ── Tokens ───────────────────────────────────────────────────────

// MCPTokenPair is one issuance: a short-lived access token and the refresh
// token that replaces it. Both exist in plaintext only in the token response.
type MCPTokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scopes       []string
}

// IssueMCPTokens mints an access/refresh pair for a grant. familyID ties the
// refresh token to its ancestors; pass "" to start a new family.
//
// Token lifetimes are clamped to the grant's own expiry, so no token can
// outlive the delegation that authorized it.
func (s *Store) IssueMCPTokens(ctx context.Context, grant *MCPGrant, scopes []string, familyID string) (*MCPTokenPair, error) {
	if len(scopes) == 0 {
		scopes = grant.Scopes
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	pair, err := insertMCPTokenPair(ctx, tx, grant, scopes, familyID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return pair, nil
}

// insertMCPTokenPair writes one access/refresh pair inside the caller's
// transaction, so an issuance commits together with whatever consumed the
// credential it replaces — or not at all.
func insertMCPTokenPair(ctx context.Context, tx *sql.Tx, grant *MCPGrant, scopes []string, familyID string, now time.Time) (*MCPTokenPair, error) {
	if !grant.Active(now) || len(scopes) == 0 {
		return nil, ErrMCPTokenInvalid
	}
	if familyID == "" {
		familyID = uuid.NewString()
	}

	accessExpiry := earliest(now.Add(MCPAccessTokenLifetime), grant.ExpiresAt)
	refreshExpiry := earliest(now.Add(MCPRefreshTokenLifetime), grant.ExpiresAt)

	access, accessHash, err := generateMCPToken(MCPAccessPrefix)
	if err != nil {
		return nil, err
	}
	refresh, refreshHash, err := generateMCPToken(MCPRefreshPrefix)
	if err != nil {
		return nil, err
	}

	insert := `INSERT INTO mcp_tokens (id, grant_id, kind, token_hash, family_id, resource, scopes, issued_at, expires_at)
	           VALUES (?,?,?,?,?,?,?,?,?)`
	if _, err := tx.ExecContext(ctx, insert, uuid.NewString(), grant.ID, "access", accessHash,
		familyID, grant.Resource, strArray(scopes), now, accessExpiry); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, insert, uuid.NewString(), grant.ID, "refresh", refreshHash,
		familyID, grant.Resource, strArray(scopes), now, refreshExpiry); err != nil {
		return nil, err
	}

	return &MCPTokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(accessExpiry.Sub(now).Seconds()),
		Scopes:       scopes,
	}, nil
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// AuthenticateMCPAccessToken resolves a presented access token to its grant and
// live owner. Every rejection returns ErrMCPTokenInvalid so a client cannot tell
// "unknown" from "expired" from "revoked" from "wrong audience".
//
// resource is the canonical MCP endpoint this request arrived at. A token minted
// for a different resource is rejected outright: that is the audience binding
// that stops a token issued for one deployment being replayed against another.
func (s *Store) AuthenticateMCPAccessToken(ctx context.Context, presented, resource string) (*MCPPrincipal, error) {
	if !strings.HasPrefix(presented, MCPAccessPrefix) || len(presented) <= len(MCPAccessPrefix) {
		return nil, ErrMCPTokenInvalid
	}
	var grantID, tokenResource string
	var scopes strArray
	var expiresAt time.Time
	var revokedAt *time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT grant_id, resource, scopes, expires_at, revoked_at
		   FROM mcp_tokens WHERE token_hash = ? AND kind = 'access'`, HashMCPToken(presented),
	).Scan(&grantID, &tokenResource, &scopes, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPTokenInvalid
	}
	if err != nil {
		slog.Warn("mcp token lookup failed", "error", err)
		return nil, ErrMCPTokenInvalid
	}
	now := time.Now()
	if revokedAt != nil || !expiresAt.After(now) || tokenResource != resource {
		return nil, ErrMCPTokenInvalid
	}

	grant, err := s.getMCPGrant(ctx, grantID)
	if err != nil || !grant.Active(now) || grant.Resource != resource {
		return nil, ErrMCPTokenInvalid
	}
	// Re-normalize what was stored. A row edited outside the API — or written by
	// an older, looser version — must not widen a grant's authority.
	grantScopes, err := NormalizeMCPScopes(grant.Scopes)
	if err != nil {
		slog.Warn("mcp grant has unusable scopes", "grant_id", grant.ID, "error", err)
		return nil, ErrMCPTokenInvalid
	}
	grant.Scopes = grantScopes

	// The token's scopes are a subset of the grant's, and the grant is
	// authoritative: narrowing a grant must narrow tokens already issued.
	tokenScopes := intersectScopes([]string(scopes), grantScopes)
	if len(tokenScopes) == 0 {
		return nil, ErrMCPTokenInvalid
	}

	user, err := s.GetUserByID(ctx, grant.UserID)
	if err != nil || user == nil {
		// The owner is gone (or unreadable): the grant acts as nobody.
		return nil, ErrMCPTokenInvalid
	}
	return &MCPPrincipal{Grant: grant, User: user, Scopes: tokenScopes, TokenExpiresAt: expiresAt}, nil
}

func intersectScopes(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if inB[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// RotateMCPRefreshToken exchanges a refresh token for a fresh pair.
//
// Rotation consumes the presented token, retires the family's access tokens,
// and mints the replacement in one transaction: a failure anywhere leaves the
// presented token unconsumed, so the client's retry is an ordinary rotation
// rather than something that looks like a replay.
//
// A token presented again is judged by how long ago it was consumed. Within
// MCPRefreshReuseGrace it is a retry or a concurrent session, not a leak, and
// it gets a sibling pair in the same family without disturbing the pair the
// first presentation received. After the grace it is a replay: the only safe
// reading is that the credential leaked, so the grant and everything under it
// is revoked.
//
// clientID is required and is bound to the grant *before* anything is consumed
// or revoked. These clients are public, so a caller that names the wrong client
// has not proven anything about the token; letting such a request rotate the
// pair (or trip the replay revocation) would hand any party who observed a
// refresh token a way to retire a delegation it does not own.
func (s *Store) RotateMCPRefreshToken(ctx context.Context, presented, clientID, resource string) (*MCPGrant, *MCPTokenPair, error) {
	if !strings.HasPrefix(presented, MCPRefreshPrefix) || len(presented) <= len(MCPRefreshPrefix) {
		return nil, nil, ErrMCPTokenInvalid
	}
	if clientID == "" {
		return nil, nil, ErrMCPTokenInvalid
	}
	hash := HashMCPToken(presented)

	// At most two passes: a presentation that loses the consuming UPDATE to a
	// concurrent one re-reads the row and is settled as a reuse.
	for range 2 {
		var id, grantID, familyID, tokenResource string
		var scopes strArray
		var expiresAt time.Time
		var revokedAt, usedAt *time.Time
		err := s.db.QueryRowContext(ctx,
			`SELECT id, grant_id, family_id, resource, scopes, expires_at, revoked_at, used_at
			   FROM mcp_tokens WHERE token_hash = ? AND kind = 'refresh'`, hash,
		).Scan(&id, &grantID, &familyID, &tokenResource, &scopes, &expiresAt, &revokedAt, &usedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrMCPTokenInvalid
		}
		if err != nil {
			slog.Warn("mcp refresh lookup failed", "error", err)
			return nil, nil, ErrMCPTokenInvalid
		}
		now := time.Now()
		// Bind the caller to the delegation first: nothing below this point is
		// free of side effects, so a request that fails the binding must fail
		// before the replay revocation and before the token is consumed.
		grant, err := s.getMCPGrant(ctx, grantID)
		if err != nil || !grant.Active(now) || grant.Resource != resource || grant.ClientID != clientID {
			return nil, nil, ErrMCPTokenInvalid
		}
		if tokenResource != resource || !expiresAt.After(now) {
			return nil, nil, ErrMCPTokenInvalid
		}

		if usedAt != nil {
			if now.Sub(*usedAt) > MCPRefreshReuseGrace {
				if rerr := s.revokeMCPGrantByID(ctx, grantID); rerr != nil {
					slog.Error("revoking grant after refresh replay failed", "grant_id", grantID, "error", rerr)
				}
				return nil, nil, ErrMCPRefreshReplayed
			}
			slog.Info("mcp: refresh token presented again within the reuse grace", "grant_id", grantID)
			return s.rotateMCPRefreshTx(ctx, "", grantID, familyID, clientID, resource, scopes)
		}
		if revokedAt != nil {
			return nil, nil, ErrMCPTokenInvalid
		}

		g, pair, err := s.rotateMCPRefreshTx(ctx, id, grantID, familyID, clientID, resource, scopes)
		if errors.Is(err, errMCPRefreshLostRace) {
			continue
		}
		return g, pair, err
	}
	return nil, nil, ErrMCPTokenInvalid
}

// rotateMCPRefreshTx performs the writes of one rotation atomically. consumeID
// is the refresh token being spent; "" issues a sibling pair for a reuse inside
// the grace, which consumes nothing and leaves the family's live access tokens
// alone — the presentation that did consume the token owns those.
//
// The grant is re-read inside the transaction: it may have been revoked since
// the caller checked it, and a token consumed under a dead grant must not yield
// a fresh pair.
func (s *Store) rotateMCPRefreshTx(ctx context.Context, consumeID, grantID, familyID, clientID, resource string, scopes []string) (*MCPGrant, *MCPTokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()

	if consumeID != "" {
		res, err := tx.ExecContext(ctx,
			`UPDATE mcp_tokens SET used_at = ?, revoked_at = ? WHERE id = ? AND used_at IS NULL`,
			now, now, consumeID)
		if err != nil {
			return nil, nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, nil, errMCPRefreshLostRace
		}
	}

	grant, err := scanMCPGrant(tx.QueryRowContext(ctx,
		`SELECT `+mcpGrantColumns+` FROM mcp_grants WHERE id = ?`, grantID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrMCPTokenInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	if !grant.Active(now) || grant.Resource != resource || grant.ClientID != clientID {
		return nil, nil, ErrMCPTokenInvalid
	}

	if consumeID != "" {
		// The old access tokens in this family are superseded. Revoking them
		// keeps "one live access token per chain" true, which makes an incident
		// easier to reason about and costs a legitimate client nothing.
		if _, err := tx.ExecContext(ctx,
			`UPDATE mcp_tokens SET revoked_at = ? WHERE family_id = ? AND kind = 'access' AND revoked_at IS NULL`,
			now, familyID); err != nil {
			return nil, nil, err
		}
	}

	// The grant is authoritative: narrowing it narrows every later issuance,
	// and a chain with nothing left in common with it gets nothing.
	pair, err := insertMCPTokenPair(ctx, tx, grant, intersectScopes(scopes, grant.Scopes), familyID, now)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return grant, pair, nil
}

// RevokeMCPTokenByValue implements RFC 7009 for a presented access or refresh
// token. Revoking a refresh token retires the whole delegation, which is what
// a client signing out actually means; revoking an access token retires only
// that token so a refresh can still recover.
//
// clientID must name the client the token was issued to. These clients are
// public, so client_id is the only thing tying the request to the delegation —
// the same binding refresh rotation requires, for the same reason: without it,
// anyone who observed a refresh token could retire a delegation they do not
// own. A mismatch is a silent no-op.
//
// It never reports whether the token existed: RFC 7009 requires a 200 either
// way, and saying more would turn the endpoint into an oracle.
func (s *Store) RevokeMCPTokenByValue(ctx context.Context, presented, clientID string) error {
	if clientID == "" {
		return nil
	}
	hash := HashMCPToken(presented)
	var grantID, kind, grantClientID string
	err := s.db.QueryRowContext(ctx,
		`SELECT t.grant_id, t.kind, g.client_id
		   FROM mcp_tokens t JOIN mcp_grants g ON g.id = t.grant_id
		  WHERE t.token_hash = ?`, hash).Scan(&grantID, &kind, &grantClientID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if grantClientID != clientID {
		return nil
	}
	if kind == "refresh" {
		return s.revokeMCPGrantByID(ctx, grantID)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		time.Now().UTC(), hash)
	return err
}

// TouchMCPGrant records that a delegation was just used, but only when the
// stored metadata is actually stale or the source address changed — so a
// polling agent costs no writes in the common case.
func (s *Store) TouchMCPGrant(ctx context.Context, g *MCPGrant, ip string) error {
	if g == nil {
		return nil
	}
	now := time.Now()
	fresh := g.LastUsedAt != nil && now.Sub(*g.LastUsedAt) < mcpGrantUsageStale
	sameIP := (g.LastUsedIP == nil && ip == "") || (g.LastUsedIP != nil && *g.LastUsedIP == ip)
	if fresh && sameIP {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE mcp_grants SET last_used_at = ?, last_used_ip = ? WHERE id = ?`,
		now.UTC(), nullIfEmpty(ip), g.ID)
	return err
}
