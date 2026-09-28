package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

const (
	mcpTestSecret   = "mcp-approval-test-secret"
	mcpTestPassword = "correct-horse-battery-staple"
)

// mcpEnv is the minimum world an approval test needs: an owner with a real
// password hash, a server, and a grant that can request lifecycle actions.
type mcpEnv struct {
	store    *store.Store
	router   http.Handler
	token    string
	ownerID  string
	serverID string
	grantID  string
}

func newMCPEnv(t *testing.T) *mcpEnv {
	t.Helper()
	ctx := context.Background()

	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	s := store.New(db).WithEncryption("master")

	hash, err := auth.HashPassword(mcpTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", hash, "admin")
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "node-secret")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID: node.ID, OwnerID: owner.ID, Name: "alpha", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/alpha", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.RegisterMCPClient(ctx, "Claude Code",
		[]string{"http://127.0.0.1:9876/callback"}, "dynamic", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	scopes := []string{string(store.MCPScopeActionsRequest)}
	req, err := s.CreateMCPAuthorizationRequest(ctx, &store.MCPAuthorizationRequest{
		ClientID:            client.ClientID,
		RedirectURI:         client.RedirectURIs[0],
		CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		CodeChallengeMethod: "S256",
		Scopes:              scopes,
		Resource:            "https://panel.example.com/api/v1/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, _, err := s.ApproveMCPAuthorization(ctx, req.ID, owner.ID, scopes,
		[]string{srv.ID}, time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	h := NewMCPGrantHandlers(s, publicurl.Config{}, nil)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(mcpTestSecret, auth.NewTicketStore(), nil))
		r.Route("/mcp/grants", func(r chi.Router) {
			r.Get("/", h.ListGrants)
			r.Patch("/{id}/approval-settings", h.UpdateGrantApprovalSettings)
		})
		r.Route("/mcp/action-requests", func(r chi.Router) {
			r.Post("/{id}/approve", h.ApproveAction)
			r.Post("/{id}/deny", h.DenyAction)
		})
		r.Route("/mcp/approval-settings", func(r chi.Router) {
			r.Get("/", h.GetApprovalSettings)
			r.Put("/", h.UpdateApprovalSettings)
		})
	})

	tok, err := auth.IssueAccessToken(mcpTestSecret, owner.ID, "owner@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return &mcpEnv{store: s, router: r, token: tok, ownerID: owner.ID, serverID: srv.ID, grantID: grant.ID}
}

func (e *mcpEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *mcpEnv) fileRequest(t *testing.T, action string) string {
	t.Helper()
	req, err := e.store.CreateMCPActionRequest(context.Background(),
		e.grantID, e.ownerID, e.serverID, action, "the server is unresponsive")
	if err != nil {
		t.Fatal(err)
	}
	return req.ID
}

// The default is unchanged from what 027 shipped: no password, no approval.
func TestApproveStillRequiresAPasswordByDefault(t *testing.T) {
	e := newMCPEnv(t)
	id := e.fileRequest(t, "restart")

	if w := e.do(t, "POST", "/mcp/action-requests/"+id+"/approve", map[string]any{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("approve with no password = %d, want 401", w.Code)
	}
	if w := e.do(t, "POST", "/mcp/action-requests/"+id+"/approve",
		map[string]any{"password": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("approve with a wrong password = %d, want 401", w.Code)
	}

	// The request is untouched by the failed attempts.
	req, err := e.store.GetMCPActionRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.EffectiveStatus(time.Now()); got != store.MCPActionPending {
		t.Fatalf("status after failed approvals = %q, want pending", got)
	}
}

// With the step-up turned off, approving is a single click — and the server
// decides that from the stored policy, not from anything the client sends.
func TestApproveSkipsThePasswordWhenPolicyAllows(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	if err := e.store.SetUserApprovalSettings(ctx, e.ownerID,
		store.ApprovalPolicy{RequirePassword: false}); err != nil {
		t.Fatal(err)
	}
	id := e.fileRequest(t, "restart")

	w := e.do(t, "POST", "/mcp/action-requests/"+id+"/approve", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("approve without a password = %d, want 200: %s", w.Code, w.Body.String())
	}
	// It really ran: the request left pending. (Execution fails at the node,
	// which is fine — what matters is that the gate did not stop it.)
	req, err := e.store.GetMCPActionRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.EffectiveStatus(time.Now()); got == store.MCPActionPending {
		t.Fatal("the request is still pending; the approval never reached execution")
	}
}

// A grant override is enough on its own to drop the step-up for one connection.
func TestGrantOverrideDropsThePasswordForThatConnectionOnly(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	if err := e.store.SetGrantPolicyOverrides(ctx, e.grantID, e.ownerID,
		store.GrantPolicyOverrides{RequirePassword: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	id := e.fileRequest(t, "restart")

	if w := e.do(t, "POST", "/mcp/action-requests/"+id+"/approve", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("approve = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// The control that protects everything else has to protect itself: turning it
// off is exactly the act an attacker on a live session would perform first.
func TestRelaxingApprovalSettingsRequiresAStepUp(t *testing.T) {
	e := newMCPEnv(t)

	for _, relaxation := range []map[string]any{
		{"require_password": false},
		{"require_password": true, "auto_approve_lifecycle": true},
		{"require_password": true, "auto_approve_upgrades": true},
	} {
		if w := e.do(t, "PUT", "/mcp/approval-settings", relaxation); w.Code != http.StatusUnauthorized {
			t.Fatalf("relaxing %v without a password = %d, want 401", relaxation, w.Code)
		}
	}

	// Nothing was written.
	policy, err := e.store.GetUserApprovalSettings(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if policy != store.SecureApprovalPolicy() {
		t.Fatalf("policy = %+v, want it unchanged after refused writes", policy)
	}

	// With the password, it goes through.
	w := e.do(t, "PUT", "/mcp/approval-settings", map[string]any{
		"require_password": false, "password": mcpTestPassword,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("relaxing with a password = %d, want 200: %s", w.Code, w.Body.String())
	}
	policy, err = e.store.GetUserApprovalSettings(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.RequirePassword {
		t.Fatal("the relaxation was accepted but not stored")
	}
}

// Tightening must never be harder than leaving things loose — a control nobody
// can turn back on without ceremony is one that stays off.
func TestTighteningApprovalSettingsNeedsNoStepUp(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	if err := e.store.SetUserApprovalSettings(ctx, e.ownerID, store.ApprovalPolicy{
		RequirePassword: false, AutoApproveLifecycle: true, AutoApproveUpgrades: true,
	}); err != nil {
		t.Fatal(err)
	}

	w := e.do(t, "PUT", "/mcp/approval-settings", map[string]any{"require_password": true})
	if w.Code != http.StatusOK {
		t.Fatalf("tightening = %d, want 200: %s", w.Code, w.Body.String())
	}
	policy, err := e.store.GetUserApprovalSettings(ctx, e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if policy != store.SecureApprovalPolicy() {
		t.Fatalf("policy = %+v, want fully tightened", policy)
	}
}

// The same rule applies one layer down, and it is measured against what the
// connection resolves to rather than the raw override.
func TestRelaxingGrantOverridesRequiresAStepUp(t *testing.T) {
	e := newMCPEnv(t)

	w := e.do(t, "PATCH", "/mcp/grants/"+e.grantID+"/approval-settings",
		map[string]any{"auto_approve_upgrades": true})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("relaxing a grant without a password = %d, want 401", w.Code)
	}

	w = e.do(t, "PATCH", "/mcp/grants/"+e.grantID+"/approval-settings",
		map[string]any{"auto_approve_upgrades": true, "password": mcpTestPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("relaxing with a password = %d, want 200: %s", w.Code, w.Body.String())
	}
	policy, err := e.store.ResolveApprovalPolicy(context.Background(), e.ownerID, e.grantID)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.AutoApproveUpgrades {
		t.Fatal("the override was accepted but not stored")
	}
}

// A grant belonging to somebody else must read as absent, not as forbidden.
func TestGrantOverridesAreScopedToTheOwner(t *testing.T) {
	e := newMCPEnv(t)
	w := e.do(t, "PATCH", "/mcp/grants/not-a-real-grant/approval-settings",
		map[string]any{"require_password": true})
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown grant = %d, want 404", w.Code)
	}
}

// Declining never gets friction, whatever the policy says.
func TestDenyNeverAsksForAPassword(t *testing.T) {
	e := newMCPEnv(t)
	id := e.fileRequest(t, "restart")

	if w := e.do(t, "POST", "/mcp/action-requests/"+id+"/deny", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("deny = %d, want 200: %s", w.Code, w.Body.String())
	}
	req, err := e.store.GetMCPActionRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.EffectiveStatus(time.Now()); got != store.MCPActionDenied {
		t.Fatalf("status = %q, want denied", got)
	}
}

// The client renders its buttons from this field, so it has to reflect the
// resolved policy rather than a guess.
func TestGrantListReportsResolvedPolicyAndOverrides(t *testing.T) {
	ctx := context.Background()
	e := newMCPEnv(t)
	if err := e.store.SetUserApprovalSettings(ctx, e.ownerID,
		store.ApprovalPolicy{RequirePassword: false, AutoApproveLifecycle: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetGrantPolicyOverrides(ctx, e.grantID, e.ownerID,
		store.GrantPolicyOverrides{AutoApproveLifecycle: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}

	w := e.do(t, "GET", "/mcp/grants", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	var got []grantView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("grants = %d, want 1", len(got))
	}
	if got[0].Policy.RequirePassword {
		t.Error("resolved policy should inherit the account default (no step-up)")
	}
	if got[0].Policy.AutoApproveLifecycle {
		t.Error("resolved policy should honor the grant's stricter override")
	}
	if got[0].Overrides.RequirePassword != nil {
		t.Error("require_password override should read as inherit")
	}
	if got[0].Overrides.AutoApproveLifecycle == nil || *got[0].Overrides.AutoApproveLifecycle {
		t.Error("auto_approve_lifecycle override should read as an explicit false")
	}
}

func boolPtr(b bool) *bool { return &b }
