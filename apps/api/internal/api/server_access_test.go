package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func accessTestStore(t *testing.T) (*store.Store, string, string, string, string) {
	t.Helper()
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
	s := store.New(db)
	ctx := context.Background()
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateUser(ctx, "admin@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID:        node.ID,
		OwnerID:       owner.ID,
		Name:          "survival",
		Platform:      "paper",
		MCVersion:     "1.21.4",
		DirectoryPath: "servers/survival",
		JavaBinary:    "java",
		Port:          25565,
		RAMMbMin:      512,
		RAMMbMax:      2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, srv.ID, owner.ID, other.ID, admin.ID
}

func TestRequireServerAccessAllowsAdminAndOwnerOnly(t *testing.T) {
	s, serverID, ownerID, otherID, adminID := accessTestStore(t)
	secret := "secret"
	r := chi.NewRouter()
	r.Use(auth.Middleware(secret, auth.NewTicketStore()))
	r.With(requireServerAccess(s)).Get("/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	cases := []struct {
		name   string
		userID string
		role   string
		want   int
	}{
		{"admin", adminID, "admin", http.StatusOK},
		{"owner", ownerID, "user", http.StatusOK},
		{"other", otherID, "user", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := auth.IssueAccessToken(secret, tc.userID, tc.name+"@example.com", tc.role)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/servers/"+serverID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestRequireServerPermissionIsGranular(t *testing.T) {
	s, serverID, _, otherID, _ := accessTestStore(t)
	secret := "secret"
	r := chi.NewRouter()
	r.Use(auth.Middleware(secret, auth.NewTicketStore()))
	r.With(requireServerPermission(s, store.ServerPermissionView)).Get("/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	r.With(requireServerPermission(s, store.ServerPermissionPower)).Post("/servers/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	r.With(requireServerPermission(s, store.ServerPermissionConsole)).Post("/servers/{id}/command", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	token, err := auth.IssueAccessToken(secret, otherID, "other@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	if err := s.SetServerPermissions(context.Background(), serverID, otherID, []string{"view"}); err != nil {
		t.Fatal(err)
	}
	if got := do(http.MethodGet, "/servers/"+serverID); got != http.StatusOK {
		t.Fatalf("view route status=%d want 200", got)
	}
	if got := do(http.MethodPost, "/servers/"+serverID+"/start"); got != http.StatusForbidden {
		t.Fatalf("power route with view status=%d want 403", got)
	}
	if err := s.SetServerPermissions(context.Background(), serverID, otherID, []string{"view", "power"}); err != nil {
		t.Fatal(err)
	}
	if got := do(http.MethodPost, "/servers/"+serverID+"/start"); got != http.StatusOK {
		t.Fatalf("power route status=%d want 200", got)
	}
	if got := do(http.MethodPost, "/servers/"+serverID+"/command"); got != http.StatusForbidden {
		t.Fatalf("console route without console status=%d want 403", got)
	}
}

func TestRequireServerPermissionLeafGranularity(t *testing.T) {
	s, serverID, _, otherID, _ := accessTestStore(t)
	secret := "secret"
	ok := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
	r := chi.NewRouter()
	r.Use(auth.Middleware(secret, auth.NewTicketStore()))
	r.With(requireServerPermission(s, store.ServerPermissionPowerStart)).Post("/servers/{id}/start", ok)
	r.With(requireServerPermission(s, store.ServerPermissionPowerStop)).Post("/servers/{id}/stop", ok)
	r.With(requireServerGroupAccess(s, store.ServerPermissionFiles)).Get("/servers/{id}/files", ok)
	r.With(requireServerPermission(s, store.ServerPermissionFilesWrite)).Put("/servers/{id}/files", ok)

	token, err := auth.IssueAccessToken(secret, otherID, "other@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	// Grant only "start the server" and "read files".
	if err := s.SetServerPermissions(context.Background(), serverID, otherID, []string{"power.start", "files.read"}); err != nil {
		t.Fatal(err)
	}
	if got := do(http.MethodPost, "/servers/"+serverID+"/start"); got != http.StatusOK {
		t.Fatalf("power.start should allow /start: status=%d", got)
	}
	if got := do(http.MethodPost, "/servers/"+serverID+"/stop"); got != http.StatusForbidden {
		t.Fatalf("power.start must not allow /stop: status=%d want 403", got)
	}
	if got := do(http.MethodGet, "/servers/"+serverID+"/files"); got != http.StatusOK {
		t.Fatalf("files.read should allow file listing: status=%d", got)
	}
	if got := do(http.MethodPut, "/servers/"+serverID+"/files"); got != http.StatusForbidden {
		t.Fatalf("files.read must not allow writes: status=%d want 403", got)
	}
}

func TestPlayerActionPermissionIsEnforced(t *testing.T) {
	s, serverID, _, otherID, _ := accessTestStore(t)
	secret := "secret"
	r := NewRouter(s, secret, "", nil)
	token, err := auth.IssueAccessToken(secret, otherID, "other@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	post := func(action string) int {
		b, _ := json.Marshal(map[string]string{"action": action, "name": "Steve"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+serverID+"/players/action", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	// A whitelist-only helper can whitelist but not ban. Allowed actions clear
	// the permission gate and then fail downstream at the (absent) agent, so the
	// meaningful assertion is "not 403".
	if err := s.SetServerPermissions(context.Background(), serverID, otherID, []string{"players.whitelist"}); err != nil {
		t.Fatal(err)
	}
	if got := post("ban"); got != http.StatusForbidden {
		t.Fatalf("players.whitelist must not permit ban: status=%d want 403", got)
	}
	if got := post("whitelist_add"); got == http.StatusForbidden {
		t.Fatalf("players.whitelist should permit whitelist_add, got 403")
	}
	if got := post("not_a_real_action"); got != http.StatusBadRequest {
		t.Fatalf("unknown action should be 400, got %d", got)
	}
}

func TestServerMembersFlowAndStaleUpdate(t *testing.T) {
	s, serverID, ownerID, otherID, _ := accessTestStore(t)
	secret := "secret"
	r := NewRouter(s, secret, "", nil)
	ownerToken, err := auth.IssueAccessToken(secret, ownerID, "owner@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	reqWithOwner := func(method, path string, body any) *httptest.ResponseRecorder {
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}

	add := reqWithOwner(http.MethodPost, "/api/v1/servers/"+serverID+"/members", map[string]any{
		"email":       "OTHER@example.com",
		"permissions": []string{"view", "players"},
	})
	if add.Code != http.StatusCreated {
		t.Fatalf("add status=%d body=%s", add.Code, add.Body.String())
	}

	stale := reqWithOwner(http.MethodPut, "/api/v1/servers/"+serverID+"/members/"+otherID, map[string]any{
		"permissions":          []string{"view", "power"},
		"expected_permissions": []string{"view"},
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update status=%d want 409 body=%s", stale.Code, stale.Body.String())
	}

	update := reqWithOwner(http.MethodPut, "/api/v1/servers/"+serverID+"/members/"+otherID, map[string]any{
		"permissions":          []string{"view", "power"},
		"expected_permissions": []string{"players", "view"},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}

	otherToken, err := auth.IssueAccessToken(secret, otherID, "other@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/servers/"+serverID+"/members/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+otherToken)
	me := httptest.NewRecorder()
	r.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK {
		t.Fatalf("members/me status=%d body=%s", me.Code, me.Body.String())
	}

	del := reqWithOwner(http.MethodDelete, "/api/v1/servers/"+serverID+"/members/"+otherID, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", del.Code, del.Body.String())
	}
	me = httptest.NewRecorder()
	r.ServeHTTP(me, meReq)
	if me.Code != http.StatusForbidden {
		t.Fatalf("members/me after revoke status=%d want 403 body=%s", me.Code, me.Body.String())
	}

	audits, err := s.ListAudit(context.Background(), serverID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawAdd, sawUpdate, sawRemove bool
	for _, entry := range audits {
		switch entry.Action {
		case "server.member.add":
			sawAdd = true
		case "server.member.update":
			sawUpdate = true
		case "server.member.remove":
			sawRemove = true
		}
	}
	if !sawAdd || !sawUpdate || !sawRemove {
		t.Fatalf("missing member audit events: add=%v update=%v remove=%v", sawAdd, sawUpdate, sawRemove)
	}
}

func TestServerMemberSecurityEdges(t *testing.T) {
	s, serverID, ownerID, _, _ := accessTestStore(t)
	secret := "secret"
	r := NewRouter(s, secret, "", nil)
	ownerToken, err := auth.IssueAccessToken(secret, ownerID, "owner@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+serverID+"/members", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}

	// A missing account must not be distinguishable from one the caller may not
	// add: same 404, same generic message — no account-enumeration oracle.
	missing := post(map[string]any{"email": "ghost@nowhere.test", "permissions": []string{"view"}})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown email status=%d want 404 body=%s", missing.Code, missing.Body.String())
	}
	if !strings.Contains(missing.Body.String(), "user not found or cannot be added") {
		t.Fatalf("unknown email leaked detail: %s", missing.Body.String())
	}

	// The owner already has implicit full access; they can't be a member row.
	owner := post(map[string]any{"email": "owner@example.com", "permissions": []string{"view"}})
	if owner.Code != http.StatusBadRequest {
		t.Fatalf("owner-as-member status=%d want 400 body=%s", owner.Code, owner.Body.String())
	}

	// Unknown permission enum is rejected.
	badPerm := post(map[string]any{"email": "other@example.com", "permissions": []string{"view", "root"}})
	if badPerm.Code != http.StatusBadRequest {
		t.Fatalf("invalid permission status=%d want 400 body=%s", badPerm.Code, badPerm.Body.String())
	}

	// Empty permission set is rejected (a member must grant at least one thing).
	empty := post(map[string]any{"email": "other@example.com", "permissions": []string{}})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty permissions status=%d want 400 body=%s", empty.Code, empty.Body.String())
	}

	// None of the rejected attempts should have written an audit row.
	audits, err := s.ListAudit(context.Background(), serverID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range audits {
		if entry.Action == "server.member.add" {
			t.Fatalf("rejected member-add wrote an audit row: %+v", entry)
		}
	}
}
