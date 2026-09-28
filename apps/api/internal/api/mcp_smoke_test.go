package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// End-to-end checks against the *mounted* endpoint, over real HTTP.
//
// The facade's own tests drive an in-memory transport, which proves the tool
// logic but skips everything this file is about: the bearer middleware, the
// audience binding, the discovery documents a client needs to find the
// authorization server, and whether a client pinned to an older protocol
// revision can still negotiate. Those live in the mount, not the facade, and a
// mistake in any of them is invisible until a real client fails to connect.
//
// The public origin resolves here because httptest listens on loopback, which
// is the same path `make dev-api` and the local demo take.

// mcpEnv is a running API with one delegation, reachable over HTTP.
type mcpEnv struct {
	*keyEnv
	server   *httptest.Server
	resource string
	grant    *store.MCPGrant
	token    string
}

func newMCPEnv(t *testing.T) *mcpEnv {
	t.Helper()
	// The MCP surface reads its origin from the environment first. Clearing it
	// keeps the loopback fallback in play regardless of the developer's shell.
	t.Setenv("MCP_PUBLIC_ORIGIN", "")
	t.Setenv("APP_ORIGIN", "")

	e := newKeyEnv(t)
	srv := httptest.NewServer(e.router)
	t.Cleanup(srv.Close)

	env := &mcpEnv{keyEnv: e, server: srv, resource: srv.URL + publicurl.MCPResourcePath}
	env.grant = env.mkGrant(t, e.ownerID, []string{e.serverA}, store.AllMCPScopes()...)
	env.token = env.mintToken(t, env.grant)
	return env
}

// mkGrant approves a delegation directly through the store, which is where the
// consent handler leaves one.
func (e *mcpEnv) mkGrant(t *testing.T, userID string, serverIDs []string, scopes ...string) *store.MCPGrant {
	t.Helper()
	ctx := context.Background()
	client, err := e.store.RegisterMCPClient(ctx, "Claude Code",
		[]string{"http://127.0.0.1:9876/cb"}, "dynamic", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	req, err := e.store.CreateMCPAuthorizationRequest(ctx, &store.MCPAuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: "http://127.0.0.1:9876/cb",
		CodeChallenge: "challenge", CodeChallengeMethod: "S256",
		Scopes: scopes, Resource: e.resource,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, _, err := e.store.ApproveMCPAuthorization(ctx, req.ID, userID, scopes, serverIDs,
		time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (e *mcpEnv) mintToken(t *testing.T, grant *store.MCPGrant) string {
	t.Helper()
	pair, err := e.store.IssueMCPTokens(context.Background(), grant, grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}

// bearer adds the Authorization header the transport itself has no field for.
type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// connectMCP dials the mounted endpoint the way a real client does.
func (e *mcpEnv) connectMCP(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   e.resource,
		HTTPClient: &http.Client{Transport: bearer{token: token}},
		// The endpoint is stateless with JSON responses, so there is no
		// standalone SSE stream to open.
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// A real client must be able to initialize, list tools, and call one against
// the mounted endpoint with nothing but an access token.
func TestMCPEndpointRoundTripOverHTTP(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	session := e.connectMCP(t, e.token)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("a grant with every scope should expose tools")
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_servers",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_servers reported an error: %+v", res.Content)
	}
	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("want structured output, got %T", res.StructuredContent)
	}
	servers, ok := structured["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("want exactly the granted server, got %+v", structured["servers"])
	}
}

// An anonymous caller must get a 401 that says where to authorize. Without the
// resource-metadata pointer a client cannot discover the authorization server,
// and "connect with one command" stops working.
func TestMCPEndpointGuidesAnAnonymousClientToTheAuthorizationServer(t *testing.T) {
	e := newMCPEnv(t)

	res, err := http.Post(e.resource, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without a token, got %d", res.StatusCode)
	}
	challenge := res.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "resource_metadata") {
		t.Fatalf("401 must point at the protected-resource metadata, got %q", challenge)
	}
	if !strings.Contains(challenge, "/.well-known/oauth-protected-resource"+publicurl.MCPResourcePath) {
		t.Fatalf("challenge names the wrong metadata document: %q", challenge)
	}
}

// The discovery documents must describe this deployment, because a client reads
// them to find the endpoints it will use.
func TestMCPDiscoveryDocumentsDescribeThisDeployment(t *testing.T) {
	e := newMCPEnv(t)

	fetch := func(path string) map[string]any {
		t.Helper()
		res, err := http.Get(e.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", path, res.StatusCode)
		}
		var doc map[string]any
		if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return doc
	}

	resourceDoc := fetch("/.well-known/oauth-protected-resource" + publicurl.MCPResourcePath)
	if resourceDoc["resource"] != e.resource {
		t.Errorf("resource = %v, want %s", resourceDoc["resource"], e.resource)
	}

	asDoc := fetch("/.well-known/oauth-authorization-server")
	if asDoc["issuer"] != e.server.URL {
		t.Errorf("issuer = %v, want %s", asDoc["issuer"], e.server.URL)
	}
	if asDoc["authorization_endpoint"] != e.server.URL+"/api/v1/oauth/authorize" {
		t.Errorf("unexpected authorization endpoint: %v", asDoc["authorization_endpoint"])
	}
	if asDoc["token_endpoint"] != e.server.URL+"/api/v1/oauth/token" {
		t.Errorf("unexpected token endpoint: %v", asDoc["token_endpoint"])
	}
	// PKCE is mandatory and S256-only; advertising anything else would invite a
	// client to use it.
	methods, _ := asDoc["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", methods)
	}
}

// rawInitialize posts an initialize request at a chosen protocol version and
// returns the decoded result, bypassing the SDK client so the version can be
// pinned to revisions the SDK would never send on its own.
func rawInitialize(t *testing.T, e *mcpEnv, version string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "pinned-client", "version": "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.resource, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+e.token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("initialize at %s: status %d: %s", version, res.StatusCode, payload)
	}

	var envelope struct {
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("initialize at %s: %v (body %s)", version, err, payload)
	}
	if len(envelope.Error) > 0 {
		t.Fatalf("initialize at %s failed: %s", version, envelope.Error)
	}
	return envelope.Result
}

// A client pinned to an older revision of the spec must still connect. This is
// the assumption the design records as a hypothesis until exercised, and the
// reason the SDK was chosen — so it is worth asserting rather than trusting.
func TestMCPEndpointNegotiatesEverySupportedProtocolVersion(t *testing.T) {
	e := newMCPEnv(t)

	for _, version := range []string{
		"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05",
	} {
		t.Run(version, func(t *testing.T) {
			result := rawInitialize(t, e, version)
			negotiated, _ := result["protocolVersion"].(string)
			if negotiated == "" {
				t.Fatalf("no protocolVersion in initialize result: %+v", result)
			}
			// The server answers with the client's own version where it can, and
			// otherwise with one it supports. Either way it must never echo a
			// version outside the supported set.
			switch negotiated {
			case "2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05":
			default:
				t.Fatalf("negotiated an unsupported version %q", negotiated)
			}
			// The instructions are part of the handshake and are what tell the
			// model the returned content is untrusted.
			if instructions, _ := result["instructions"].(string); instructions == "" {
				t.Error("initialize must carry the server instructions")
			}
		})
	}
}

// A token minted for one deployment must not work against another. The audience
// binding is what stops a token leaked from a staging panel being replayed here.
func TestMCPEndpointRejectsATokenBoundToAnotherResource(t *testing.T) {
	e := newMCPEnv(t)

	// Approve a delegation whose request names a different resource entirely.
	ctx := context.Background()
	client, err := e.store.RegisterMCPClient(ctx, "Elsewhere",
		[]string{"http://127.0.0.1:9876/cb"}, "dynamic", "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	req, err := e.store.CreateMCPAuthorizationRequest(ctx, &store.MCPAuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: "http://127.0.0.1:9876/cb",
		CodeChallenge: "challenge", CodeChallengeMethod: "S256",
		Scopes: store.AllMCPScopes(), Resource: "https://other.example.com/api/v1/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := e.store.ApproveMCPAuthorization(ctx, req.ID, e.ownerID,
		store.AllMCPScopes(), []string{e.serverA}, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	assertUnauthorized(t, e, e.mintToken(t, foreign))
}

// Access keys are deliberately not an MCP credential: they are long-lived and
// copied into config files, which is the exact property this feature exists to
// avoid. Accepting one would quietly make the unsupported path the easy one.
func TestMCPEndpointRejectsAnAgentAccessKey(t *testing.T) {
	e := newMCPEnv(t)
	key := e.issueKey(t, e.ownerID, []string{"view"}, []string{e.serverA})
	assertUnauthorized(t, e, key)
}

// Revocation is the promise the whole design rests on: no cache anywhere, so a
// revoked grant stops working on the very next call rather than in 15 minutes.
func TestMCPRevocationLandsOnTheNextCall(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	session := e.connectMCP(t, e.token)

	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("the grant should work before it is revoked: %v", err)
	}
	if err := e.store.RevokeMCPGrant(ctx, e.grant.ID, e.ownerID); err != nil {
		t.Fatal(err)
	}
	// The established session keeps its transport, but the token behind it is
	// re-resolved on every request, so the next call must fail.
	if _, err := session.ListTools(ctx, nil); err == nil {
		t.Fatal("a revoked grant must stop working immediately")
	}
}

// The token endpoint must refuse a refresh request that omits or misnames
// client_id, and refuse it without spending the token: a public client's
// refresh token would otherwise be a denial-of-service handle for anyone who
// saw it once.
func TestMCPRefreshRequiresTheRightClientIDOverHTTP(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	pair, err := e.store.IssueMCPTokens(ctx, e.grant, e.grant.Scopes, "")
	if err != nil {
		t.Fatal(err)
	}

	refresh := func(clientID string) (int, map[string]any) {
		t.Helper()
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {pair.RefreshToken},
		}
		if clientID != "" {
			form.Set("client_id", clientID)
		}
		res, err := http.Post(e.server.URL+"/api/v1/oauth/token",
			"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var doc map[string]any
		if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
			t.Fatalf("decoding token response: %v", err)
		}
		return res.StatusCode, doc
	}

	if status, doc := refresh(""); status != http.StatusBadRequest || doc["error"] != "invalid_request" {
		t.Fatalf("missing client_id: want 400/invalid_request, got %d/%v", status, doc["error"])
	}
	if status, doc := refresh("mcp_client_wrong"); status != http.StatusBadRequest || doc["error"] != "invalid_grant" {
		t.Fatalf("wrong client_id: want 400/invalid_grant, got %d/%v", status, doc["error"])
	}

	// Neither refusal burned the token: the real client still gets a pair.
	status, doc := refresh(e.grant.ClientID)
	if status != http.StatusOK {
		t.Fatalf("the real client must still refresh: got %d/%v", status, doc)
	}
	if doc["access_token"] == "" || doc["refresh_token"] == "" {
		t.Fatalf("refresh response is missing material: %v", doc)
	}
	if _, err := e.store.AuthenticateMCPAccessToken(ctx, doc["access_token"].(string), e.resource); err != nil {
		t.Fatalf("the rotated access token should work: %v", err)
	}
}

// The mount owns exactly one path. The dashboard's delegation screens read
// /api/v1/mcp/connection, /mcp/grants and /mcp/action-requests — ordinary
// session-authenticated routes that happen to sit under the same prefix — and a
// wildcard mounted below the resource captured all three, so a signed-in
// operator got the 401 bearer challenge meant for agents instead of their own
// connections page.
func TestDashboardMCPRoutesAreNotCapturedByTheMCPMount(t *testing.T) {
	e := newMCPEnv(t)
	jwt := e.jwtToken(e.ownerID, "user")

	for _, path := range []string{
		"/api/v1/mcp/connection",
		"/api/v1/mcp/grants",
		"/api/v1/mcp/action-requests",
	} {
		t.Run(path, func(t *testing.T) {
			rr := e.do(http.MethodGet, path, jwt)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d want 200: %s", rr.Code, rr.Body.String())
			}
			// A 401 from the mount is accompanied by the bearer challenge, so
			// its absence is the direct evidence that MCP token verification
			// never ran — and it keeps this honest if the two paths ever agree
			// on a status code.
			if challenge := rr.Header().Get("WWW-Authenticate"); challenge != "" {
				t.Fatalf("MCP bearer authentication answered a dashboard route: %q", challenge)
			}
		})
	}

	// The reads reached their real handlers, not a stub that happens to answer
	// 200: the grant this environment approved is in the list.
	rr := e.do(http.MethodGet, "/api/v1/mcp/grants", jwt)
	var grants []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decoding grants: %v (body %s)", err, rr.Body.String())
	}
	if len(grants) != 1 || grants[0].ID != e.grant.ID {
		t.Fatalf("grants = %+v, want the approved delegation %s", grants, e.grant.ID)
	}

	// And the resource itself still belongs to the mount: a dashboard session is
	// not an MCP credential, so the same JWT is refused there — over the live
	// listener, where the loopback origin resolves and the challenge a client
	// needs is therefore complete.
	res, err := (&http.Client{Transport: bearer{token: jwt}}).Get(e.resource)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("MCP endpoint status=%d want 401 for a session token", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("the MCP endpoint stopped issuing its bearer challenge: %q",
			res.Header.Get("WWW-Authenticate"))
	}
}

func TestMCPClientIPIgnoresUntrustedForwardedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", nil)
	req.RemoteAddr = "198.51.100.27:4321"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := clientIP(req); got != "198.51.100.27" {
		t.Fatalf("clientIP = %q, want trusted RemoteAddr; forwarded header was accepted", got)
	}
}

// assertUnauthorized checks that a credential cannot even open a session.
func assertUnauthorized(t *testing.T, e *mcpEnv, token string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.resource, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("want 401, got %d: %s", res.StatusCode, body)
	}
}
