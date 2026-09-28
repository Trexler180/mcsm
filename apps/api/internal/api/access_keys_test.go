package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// keyEnv is a two-server fleet with an owner, a collaborator, and an admin —
// enough to prove that a key is bounded by its own allowlist and scopes on top
// of whatever authority its owner happens to have.
type keyEnv struct {
	store    *store.Store
	router   http.Handler
	serverA  string
	serverB  string
	ownerID  string
	otherID  string
	adminID  string
	jwtToken func(userID, role string) string
}

func newKeyEnv(t testing.TB) *keyEnv {
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
	mkUser := func(email, role string) string {
		u, err := s.CreateUser(ctx, email, "hash", role)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	ownerID := mkUser("owner@example.com", "user")
	otherID := mkUser("other@example.com", "user")
	adminID := mkUser("admin@example.com", "admin")

	mkServer := func(name string) string {
		srv, err := s.CreateServer(ctx, &store.Server{
			NodeID: node.ID, OwnerID: ownerID, Name: name, Platform: "paper",
			MCVersion: "1.21.4", DirectoryPath: "servers/" + name, JavaBinary: "java",
			Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
		})
		if err != nil {
			t.Fatal(err)
		}
		return srv.ID
	}

	secret := "secret"
	return &keyEnv{
		store:   s,
		router:  NewRouter(s, secret, "", nil, testNotifier(s)),
		serverA: mkServer("alpha"),
		serverB: mkServer("beta"),
		ownerID: ownerID,
		otherID: otherID,
		adminID: adminID,
		jwtToken: func(userID, role string) string {
			tok, err := auth.IssueAccessToken(secret, userID, "u@example.com", role)
			if err != nil {
				t.Fatal(err)
			}
			return tok
		},
	}
}

// issueKey creates a key directly through the store, which is what the
// lifecycle handler does after its own authorization checks.
func (e *keyEnv) issueKey(t testing.TB, userID string, scopes, servers []string) string {
	t.Helper()
	_, token, err := e.store.CreateAccessKey(context.Background(), userID, "agent",
		scopes, servers, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (e *keyEnv) do(method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

// send is do() with a JSON body, for the routes whose authorization depends on
// what the body asks for.
func (e *keyEnv) send(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, jsonBody(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

// An access key may only be used under /api/v1/servers. Every other route
// family — authentication, tickets, sessions, users, nodes, secrets,
// notifications, the global audit log, and key management itself — is closed,
// so a stolen key cannot administer the panel or mint a second credential.
func TestAccessKeysAreConfinedToServerRoutes(t *testing.T) {
	e := newKeyEnv(t)
	// Deliberately an admin-owned key: ownership must not widen the boundary.
	token := e.issueKey(t, e.adminID, []string{"view"}, []string{e.serverA})

	closed := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/ticket"},
		{http.MethodGet, "/api/v1/auth/sessions"},
		{http.MethodGet, "/api/v1/auth/mfa"},
		{http.MethodGet, "/api/v1/auth/api-keys"},
		{http.MethodPost, "/api/v1/auth/api-keys"},
		{http.MethodGet, "/api/v1/users"},
		{http.MethodGet, "/api/v1/nodes"},
		{http.MethodGet, "/api/v1/audit"},
		{http.MethodGet, "/api/v1/overview"},
		{http.MethodGet, "/api/v1/settings/integrations"},
		{http.MethodGet, "/api/v1/notifications/feed"},
		{http.MethodGet, "/api/v1/server-folders"},
		{http.MethodGet, "/api/v1/minecraft/versions"},
		{http.MethodGet, "/api/v1/time"},
	}
	for _, tc := range closed {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if got := e.do(tc.method, tc.path, token).Code; got != http.StatusForbidden {
				t.Fatalf("status=%d want 403 — an access key reached %s", got, tc.path)
			}
		})
	}

	// The server family itself is open (subject to the key's own bounds).
	if got := e.do(http.MethodGet, "/api/v1/servers/"+e.serverA, token).Code; got != http.StatusOK {
		t.Fatalf("allowlisted server status=%d want 200", got)
	}
	// A path that merely starts with the same characters is not the family.
	if got := e.do(http.MethodGet, "/api/v1/server-folders", token).Code; got != http.StatusForbidden {
		t.Fatalf("/api/v1/server-folders status=%d want 403", got)
	}
}

// The heart of the design: a key's server allowlist and scopes are checked
// before anything about its owner, so global-admin ownership never escapes them.
func TestAccessKeyIntersectionMatrix(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()
	// The collaborator holds a mixed set on server A only.
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID,
		[]string{"view", "power.restart", "files.read", "tasks"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		userID  string
		scopes  []string
		servers []string
		method  string
		path    string
		want    int
	}{
		{
			name:   "admin-owned key cannot leave its allowlist",
			userID: e.adminID, scopes: []string{"view"}, servers: []string{e.serverA},
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverB, want: http.StatusForbidden,
		},
		{
			name:   "admin-owned key cannot exceed its scopes",
			userID: e.adminID, scopes: []string{"view"}, servers: []string{e.serverA},
			method: http.MethodPost, path: "/api/v1/servers/" + e.serverA + "/restart", want: http.StatusForbidden,
		},
		{
			name:   "admin-owned key works inside its bounds",
			userID: e.adminID, scopes: []string{"view"}, servers: []string{e.serverA},
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverA, want: http.StatusOK,
		},
		{
			name:   "owner-owned key works inside its bounds",
			userID: e.ownerID, scopes: []string{"view"}, servers: []string{e.serverA},
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverA, want: http.StatusOK,
		},
		{
			name:   "collaborator key cannot reach a server they have no grant on",
			userID: e.otherID, scopes: []string{"view"}, servers: []string{e.serverA, e.serverB},
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverB, want: http.StatusForbidden,
		},
		{
			name:   "a scope the owner does not hold is still refused",
			userID: e.otherID, scopes: []string{"console"}, servers: []string{e.serverA},
			method: http.MethodPost, path: "/api/v1/servers/" + e.serverA + "/command", want: http.StatusForbidden,
		},
		{
			name:   "a group scope satisfies a read route",
			userID: e.otherID, scopes: []string{"tasks"}, servers: []string{e.serverA},
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverA + "/tasks", want: http.StatusOK,
		},
		{
			name:   "a leaf scope does not satisfy a sibling leaf",
			userID: e.otherID, scopes: []string{"power.restart"}, servers: []string{e.serverA},
			method: http.MethodPost, path: "/api/v1/servers/" + e.serverA + "/kill", want: http.StatusForbidden,
		},
		{
			name:   "a files.read scope opens the group's read route",
			userID: e.otherID, scopes: []string{"files.read"}, servers: []string{e.serverA},
			// The listing itself needs an agent, so the meaningful assertion is
			// that the gate let it through — see the paired 403 below.
			method: http.MethodGet, path: "/api/v1/servers/" + e.serverA + "/files", want: -1,
		},
		{
			name:   "a files.read scope does not open a write route",
			userID: e.otherID, scopes: []string{"files.read"}, servers: []string{e.serverA},
			method: http.MethodPost, path: "/api/v1/servers/" + e.serverA + "/files/mkdir", want: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := e.issueKey(t, tc.userID, tc.scopes, tc.servers)
			got := e.do(tc.method, tc.path, token).Code
			if tc.want == -1 {
				if got == http.StatusForbidden {
					t.Fatalf("status=403, want the access gate to pass the request through")
				}
				return
			}
			if got != tc.want {
				t.Fatalf("status=%d want=%d", got, tc.want)
			}
		})
	}
}

// The admin-only server routes stay closed to machine principals, which is what
// keeps creation, import, cloning, deletion and membership out of reach.
func TestAccessKeysCannotReachAdminServerRoutes(t *testing.T) {
	e := newKeyEnv(t)
	// The broadest key the store will issue for an admin: every non-admin scope
	// on both servers. It must still fail every route below.
	scopes := []string{"view", "power", "console", "players", "files", "mods", "backups", "tasks", "settings"}
	token := e.issueKey(t, e.adminID, scopes, []string{e.serverA, e.serverB})

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/servers"},
		{http.MethodGet, "/api/v1/servers/import-candidates"},
		{http.MethodPost, "/api/v1/servers/" + e.serverA + "/clone"},
		{http.MethodDelete, "/api/v1/servers/" + e.serverA},
		{http.MethodGet, "/api/v1/servers/" + e.serverA + "/members"},
		{http.MethodPost, "/api/v1/servers/" + e.serverA + "/members"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if got := e.do(tc.method, tc.path, token).Code; got != http.StatusForbidden {
				t.Fatalf("status=%d want 403", got)
			}
		})
	}

	// The admin scope cannot even be recorded on a key, so there is no way to
	// widen one into the routes above.
	if _, _, err := e.store.CreateAccessKey(context.Background(), e.adminID, "escalate",
		[]string{"admin"}, []string{e.serverA}, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("the admin scope must be refused at issue time")
	}
}

// Revocation, rotation and permission changes all take effect on the very next
// request — there is no cache to wait out.
func TestAccessKeyChangesTakeEffectImmediately(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID, []string{"view", "power.restart"}); err != nil {
		t.Fatal(err)
	}
	keys, err := e.store.ListAccessKeys(ctx, e.otherID)
	if err != nil || len(keys) != 0 {
		t.Fatalf("expected a clean slate, got %d keys / %v", len(keys), err)
	}
	token := e.issueKey(t, e.otherID, []string{"view", "power.restart"}, []string{e.serverA})
	path := "/api/v1/servers/" + e.serverA

	if got := e.do(http.MethodGet, path, token).Code; got != http.StatusOK {
		t.Fatalf("baseline status=%d want 200", got)
	}

	// Narrowing the owner's grant closes the routes they lost, on the next call,
	// even though the key still carries the scope. (`view` survives because any
	// remaining grant implies it — that is the existing user-permission rule,
	// which the key deliberately reuses rather than reinterprets.)
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID, []string{"view"}); err != nil {
		t.Fatal(err)
	}
	if got := e.do(http.MethodPost, path+"/restart", token).Code; got != http.StatusForbidden {
		t.Fatalf("after losing power.restart status=%d want 403", got)
	}

	// Removing the membership outright closes everything.
	if err := e.store.DeleteServerPermissions(ctx, e.serverA, e.otherID); err != nil {
		t.Fatal(err)
	}
	if got := e.do(http.MethodGet, path, token).Code; got != http.StatusForbidden {
		t.Fatalf("after membership removal status=%d want 403", got)
	}
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID, []string{"view", "power.restart"}); err != nil {
		t.Fatal(err)
	}

	// Rotation kills the old secret at once.
	issued, err := e.store.ListAccessKeys(ctx, e.otherID)
	if err != nil || len(issued) != 1 {
		t.Fatalf("expected exactly one key, got %d / %v", len(issued), err)
	}
	_, rotated, err := e.store.RotateAccessKey(ctx, issued[0].ID, e.otherID)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.do(http.MethodGet, path, token).Code; got != http.StatusUnauthorized {
		t.Fatalf("rotated-away token status=%d want 401", got)
	}
	if got := e.do(http.MethodGet, path, rotated).Code; got != http.StatusOK {
		t.Fatalf("rotated token status=%d want 200", got)
	}

	// So does revocation.
	if err := e.store.RevokeAccessKey(ctx, issued[0].ID, e.otherID); err != nil {
		t.Fatal(err)
	}
	if got := e.do(http.MethodGet, path, rotated).Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d want 401", got)
	}
}

// The list endpoint must not disclose servers a key cannot use, and must report
// only the authority the key actually has on the ones it can.
func TestServerListIsFilteredForAccessKeys(t *testing.T) {
	e := newKeyEnv(t)

	list := func(token string) []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	} {
		t.Helper()
		rr := e.do(http.MethodGet, "/api/v1/servers", token)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var out []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// An admin sees the whole fleet by JWT...
	if rows := list(e.jwtToken(e.adminID, "admin")); len(rows) != 2 {
		t.Fatalf("admin JWT saw %d servers, want 2", len(rows))
	}
	// ...but their key sees only what it lists, with only the scopes it holds.
	token := e.issueKey(t, e.adminID, []string{"view", "power.restart"}, []string{e.serverA})
	rows := list(token)
	if len(rows) != 1 || rows[0].ID != e.serverA {
		t.Fatalf("key saw %d servers, want only the allowlisted one: %+v", len(rows), rows)
	}
	for _, p := range rows[0].Permissions {
		if p == "admin" || p == "settings" || p == "console" {
			t.Fatalf("reported permissions leak authority the key does not have: %v", rows[0].Permissions)
		}
	}
	var sawView, sawPower bool
	for _, p := range rows[0].Permissions {
		switch p {
		case "view":
			sawView = true
		case "power":
			sawPower = true
		}
	}
	if !sawView {
		t.Fatalf("view should survive the intersection: %v", rows[0].Permissions)
	}
	// The owner's `power` group is wider than the key's `power.restart` leaf, so
	// it must not be reported.
	if sawPower {
		t.Fatalf("the group permission outranks the key's leaf scope: %v", rows[0].Permissions)
	}
}

// java_binary, jvm_args and directory_path become the agent's start command, so
// changing one is host code execution. The route gate can only prove the caller
// holds `settings`; the field-level guard inside the handler is what confines
// those three to global administrators — and it must not count a machine
// principal as one, or an admin-owned `settings` key would reach the host.
func TestAccessKeyCannotChangeHostExecutionFields(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()
	before, err := e.store.GetServer(ctx, e.serverA)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/servers/" + e.serverA

	for _, owner := range []struct {
		who    string
		userID string
	}{{"admin-owned", e.adminID}, {"server-owner-owned", e.ownerID}} {
		token := e.issueKey(t, owner.userID, []string{"view", "settings"}, []string{e.serverA})
		for _, tc := range []struct{ name, body string }{
			{"java_binary", `{"java_binary":"/tmp/payload.sh"}`},
			{"jvm_args", `{"jvm_args":["-XX:OnOutOfMemoryError=/tmp/payload.sh"]}`},
			{"directory_path", `{"directory_path":"/etc"}`},
			// A benign field alongside must not smuggle the guarded one through.
			{"mixed", `{"name":"smuggled","java_binary":"/tmp/payload.sh"}`},
		} {
			t.Run(owner.who+"/"+tc.name, func(t *testing.T) {
				if got := e.send(http.MethodPut, path, token, tc.body).Code; got != http.StatusForbidden {
					t.Fatalf("status=%d want 403 — a key changed %s", got, tc.name)
				}
			})
		}
	}

	after, err := e.store.GetServer(ctx, e.serverA)
	if err != nil {
		t.Fatal(err)
	}
	if after.JavaBinary != before.JavaBinary ||
		strings.Join(after.JVMArgs, " ") != strings.Join(before.JVMArgs, " ") ||
		after.DirectoryPath != before.DirectoryPath ||
		after.Name != before.Name {
		t.Fatalf("a refused request still mutated the server: %+v -> %+v", before, after)
	}

	// The same key still performs an ordinary settings change: the guard is
	// field-level, not a blanket ban on machine updates.
	token := e.issueKey(t, e.ownerID, []string{"view", "settings"}, []string{e.serverA})
	if got := e.send(http.MethodPut, path, token, `{"name":"alpha-renamed"}`).Code; got != http.StatusOK {
		t.Fatalf("ordinary settings change status=%d want 200", got)
	}
	// And a human administrator is unaffected.
	adminJWT := e.jwtToken(e.adminID, "admin")
	if got := e.send(http.MethodPut, path, adminJWT, `{"java_binary":"/usr/bin/java21"}`).Code; got != http.StatusOK {
		t.Fatalf("human admin status=%d want 200 — the guard caught the wrong caller", got)
	}
}

// The players route is gated on group access because the action arrives in the
// body, so the specific permission is enforced in the handler. A key's scopes
// have to satisfy it before any user-side check: a players.whitelist key must
// not op or ban, whoever owns it.
func TestAccessKeyPlayerActionsRequireTheMatchingScope(t *testing.T) {
	e := newKeyEnv(t)
	path := "/api/v1/servers/" + e.serverA + "/players/action"

	for _, owner := range []struct {
		who    string
		userID string
	}{{"admin-owned", e.adminID}, {"server-owner-owned", e.ownerID}} {
		token := e.issueKey(t, owner.userID, []string{"view", "players.whitelist"}, []string{e.serverA})
		for _, action := range []string{"op", "deop", "ban", "ban_ip", "pardon", "pardon_ip", "kick"} {
			t.Run(owner.who+"/"+action, func(t *testing.T) {
				body := `{"action":"` + action + `","player":"notch"}`
				if got := e.send(http.MethodPost, path, token, body).Code; got != http.StatusForbidden {
					t.Fatalf("status=%d want 403 — a players.whitelist key performed %s", got, action)
				}
			})
		}
		// The action it does hold gets past authorization and on to the agent,
		// which is absent here — so the assertion is "not refused".
		t.Run(owner.who+"/whitelist_add", func(t *testing.T) {
			rr := e.send(http.MethodPost, path, token, `{"action":"whitelist_add","player":"notch"}`)
			if rr.Code == http.StatusForbidden {
				t.Fatalf("the key's own action was refused: %s", rr.Body.String())
			}
		})
	}
}

// Scheduled tasks persist beyond the request that creates or updates them. A
// short-lived machine credential must not be able to convert its temporary
// authority into a recurring action that survives expiry or revocation.
func TestAccessKeysCannotCreateOrUpdateScheduledTasks(t *testing.T) {
	e := newKeyEnv(t)
	path := "/api/v1/servers/" + e.serverA + "/tasks"
	body := `{"name":"nightly","cron_expr":"0 4 * * *","action":"restart","enabled":true}`
	token := e.issueKey(t, e.ownerID, []string{"view", "tasks", "power.restart"}, []string{e.serverA})

	if rr := e.send(http.MethodPost, path, token, body); rr.Code != http.StatusForbidden {
		t.Fatalf("create status=%d want 403: %s", rr.Code, rr.Body.String())
	}
	if rr := e.send(http.MethodPut, path+"/any-task", token, `{"enabled":false}`); rr.Code != http.StatusForbidden {
		t.Fatalf("update status=%d want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestBackupTargetRetentionRequiresDeleteAuthority(t *testing.T) {
	e := newKeyEnv(t)
	path := "/api/v1/servers/" + e.serverA + "/backup-targets"
	body := `{"name":"aggressive","type":"local","config":{},"retention":{"keep_last_n":1},"is_default":true}`

	createOnly := e.issueKey(t, e.ownerID, []string{"view", "backups.create"}, []string{e.serverA})
	if rr := e.send(http.MethodPost, path, createOnly, body); rr.Code != http.StatusForbidden {
		t.Fatalf("create-only status=%d want 403: %s", rr.Code, rr.Body.String())
	}

	createAndDelete := e.issueKey(t, e.ownerID, []string{"view", "backups.create", "backups.delete"}, []string{e.serverA})
	if rr := e.send(http.MethodPost, path, createAndDelete, body); rr.Code != http.StatusCreated {
		t.Fatalf("create+delete status=%d want 201: %s", rr.Code, rr.Body.String())
	}
}

// /members/me is what the panel and any agent read to decide what to attempt. On
// a key it must describe the key — the intersection of its scopes with the
// owner's current grants — and never claim owner or global-admin authority.
func TestMembersMeReportsMachineKeyAuthority(t *testing.T) {
	e := newKeyEnv(t)
	path := "/api/v1/servers/" + e.serverA + "/members/me"

	me := func(token string) (bool, bool, []string) {
		t.Helper()
		rr := e.do(http.MethodGet, path, token)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var out struct {
			Owner       bool     `json:"owner"`
			GlobalAdmin bool     `json:"global_admin"`
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Owner, out.GlobalAdmin, out.Permissions
	}

	// The humans are unchanged: the server's owner is an owner, an admin is a
	// global admin, and both hold everything.
	if owner, admin, perms := me(e.jwtToken(e.ownerID, "user")); !owner || admin || len(perms) != len(store.AllServerPermissions()) {
		t.Fatalf("owner JWT: owner=%v global_admin=%v perms=%v", owner, admin, perms)
	}
	if owner, admin, perms := me(e.jwtToken(e.adminID, "admin")); owner || !admin || len(perms) != len(store.AllServerPermissions()) {
		t.Fatalf("admin JWT: owner=%v global_admin=%v perms=%v", owner, admin, perms)
	}

	for _, tc := range []struct {
		who    string
		userID string
		scopes []string
		want   []string
	}{
		{"server owner's key", e.ownerID, []string{"view", "power.restart"}, []string{"view", "power.restart"}},
		{"global admin's key", e.adminID, []string{"view", "files.read"}, []string{"view", "files.read"}},
	} {
		t.Run(tc.who, func(t *testing.T) {
			owner, admin, perms := me(e.issueKey(t, tc.userID, tc.scopes, []string{e.serverA}))
			if owner || admin {
				t.Fatalf("a machine principal reported owner=%v global_admin=%v", owner, admin)
			}
			if !samePermissions(perms, tc.want) {
				t.Fatalf("permissions=%v want %v", perms, tc.want)
			}
		})
	}

	// A key whose owner is only a collaborator reports the narrower of the two
	// sides, per permission — the scope it holds and the grant that backs it.
	if err := e.store.SetServerPermissions(context.Background(), e.serverA, e.otherID,
		[]string{"view", "files.read"}); err != nil {
		t.Fatal(err)
	}
	token := e.issueKey(t, e.otherID, []string{"view", "files.read", "console"}, []string{e.serverA})
	owner, admin, perms := me(token)
	if owner || admin {
		t.Fatalf("collaborator key reported owner=%v global_admin=%v", owner, admin)
	}
	if !samePermissions(perms, []string{"view", "files.read"}) {
		t.Fatalf("permissions=%v — a scope the owner does not hold must not be reported", perms)
	}
}

// The effective-permission report is built from the key's scopes, which are the
// narrower side. An owner holds the whole `power` group; a key holding only the
// `power.restart` leaf must be reported as holding that leaf — not the group it
// does not have, and not nothing at all.
func TestEffectivePermissionsReportTheKeyLeafNotTheOwnerGroup(t *testing.T) {
	e := newKeyEnv(t)
	token := e.issueKey(t, e.ownerID, []string{"view", "power.restart"}, []string{e.serverA})

	check := func(what string, perms []string) {
		t.Helper()
		if !samePermissions(perms, []string{"view", "power.restart"}) {
			t.Fatalf("%s reported %v, want exactly [view power.restart]", what, perms)
		}
	}

	rr := e.do(http.MethodGet, "/api/v1/servers", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d", rr.Code)
	}
	var rows []struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("list returned %d rows, want 1", len(rows))
	}
	check("the server list", rows[0].Permissions)

	meRR := e.do(http.MethodGet, "/api/v1/servers/"+e.serverA+"/members/me", token)
	if meRR.Code != http.StatusOK {
		t.Fatalf("members/me status=%d", meRR.Code)
	}
	var self struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(meRR.Body.Bytes(), &self); err != nil {
		t.Fatal(err)
	}
	check("members/me", self.Permissions)

	// The report is honest in both directions: the leaf it names really works,
	// and the sibling it omits really does not.
	if got := e.do(http.MethodPost, "/api/v1/servers/"+e.serverA+"/restart", token).Code; got == http.StatusForbidden {
		t.Fatal("power.restart was reported but refused")
	}
	if got := e.do(http.MethodPost, "/api/v1/servers/"+e.serverA+"/kill", token).Code; got != http.StatusForbidden {
		t.Fatalf("kill status=%d want 403 — an unreported permission worked", got)
	}
}

// The mirror image of the case above: the key holds the `power` group and the
// owner has since been narrowed to the `power.restart` leaf. The gate allows
// restart (the scope covers the leaf through its parent, the grant covers it
// directly) and refuses kill, so both reports must name exactly that leaf —
// reporting the group would overstate the key, and reporting nothing would send
// an agent past a route it is actually allowed to use.
func TestEffectivePermissionsReportTheOwnerLeafUnderAKeyGroupScope(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()
	// Issued while the collaborator still held the whole group...
	token := e.issueKey(t, e.otherID, []string{"view", "power"}, []string{e.serverA})
	// ...and narrowed afterwards, which is what makes the two sides disagree.
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID,
		[]string{"view", "power.restart"}); err != nil {
		t.Fatal(err)
	}

	check := func(what string, perms []string) {
		t.Helper()
		if !samePermissions(perms, []string{"view", "power.restart"}) {
			t.Fatalf("%s reported %v, want exactly [view power.restart]", what, perms)
		}
	}

	rr := e.do(http.MethodGet, "/api/v1/servers", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rows []struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != e.serverA {
		t.Fatalf("list returned %+v, want only the allowlisted server", rows)
	}
	check("the server list", rows[0].Permissions)

	meRR := e.do(http.MethodGet, "/api/v1/servers/"+e.serverA+"/members/me", token)
	if meRR.Code != http.StatusOK {
		t.Fatalf("members/me status=%d body=%s", meRR.Code, meRR.Body.String())
	}
	var self struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(meRR.Body.Bytes(), &self); err != nil {
		t.Fatal(err)
	}
	check("members/me", self.Permissions)

	// And the report matches the gate in both directions.
	if got := e.do(http.MethodPost, "/api/v1/servers/"+e.serverA+"/restart", token).Code; got == http.StatusForbidden {
		t.Fatal("power.restart was reported but refused")
	}
	if got := e.do(http.MethodPost, "/api/v1/servers/"+e.serverA+"/kill", token).Code; got != http.StatusForbidden {
		t.Fatalf("kill status=%d want 403 — an unreported permission worked", got)
	}
}

// samePermissions compares permission sets ignoring order.
func samePermissions(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, p := range got {
		seen[p]++
	}
	for _, p := range want {
		if seen[p] == 0 {
			return false
		}
		seen[p]--
	}
	return true
}

// A machine action is attributable to both the human owner and the exact key,
// and no part of the credential appears in the record.
func TestAccessKeyActionsAreAttributedToUserAndKey(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()

	key, token, err := e.store.CreateAccessKey(ctx, e.ownerID, "agent",
		[]string{"settings"}, []string{e.serverA}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// A settings update is audited and needs no agent.
	body := `{"name":"alpha-renamed","platform":"paper","mc_version":"1.21.4","port":25565,"ram_mb_min":512,"ram_mb_max":2048}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/servers/"+e.serverA, jsonBody(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}

	entries, err := e.store.ListAudit(ctx, e.serverA, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.AuditEntry
	for _, entry := range entries {
		if entry.Action == "server.update" {
			found = entry
			break
		}
	}
	if found == nil {
		t.Fatalf("no server.update audit entry: %+v", entries)
	}
	if found.UserID == nil || *found.UserID != e.ownerID {
		t.Fatalf("user attribution = %v, want the key owner", found.UserID)
	}
	if found.APIKeyID == nil || *found.APIKeyID != key.ID {
		t.Fatalf("key attribution = %v, want %s", found.APIKeyID, key.ID)
	}
	if found.Detail != nil && containsAny(*found.Detail, token, key.TokenPrefix) {
		t.Fatalf("audit detail leaked credential material: %s", *found.Detail)
	}

	// A human action on the same server still records no key. It has to be a
	// different change — an update that alters nothing writes no audit row.
	humanBody := `{"name":"alpha-by-hand","platform":"paper","mc_version":"1.21.4","port":25565,"ram_mb_min":512,"ram_mb_max":2048}`
	jwtReq := httptest.NewRequest(http.MethodPut, "/api/v1/servers/"+e.serverA, jsonBody(humanBody))
	jwtReq.Header.Set("Authorization", "Bearer "+e.jwtToken(e.ownerID, "user"))
	jwtReq.Header.Set("Content-Type", "application/json")
	jwtRR := httptest.NewRecorder()
	e.router.ServeHTTP(jwtRR, jwtReq)
	if jwtRR.Code != http.StatusOK {
		t.Fatalf("human update status=%d body=%s", jwtRR.Code, jwtRR.Body.String())
	}
	after, err := e.store.ListAudit(ctx, e.serverA, 20)
	if err != nil {
		t.Fatal(err)
	}
	var humanEntries int
	for _, entry := range after {
		if entry.Action == "server.update" && entry.APIKeyID == nil {
			humanEntries++
		}
	}
	if humanEntries != 1 {
		t.Fatalf("expected exactly one un-keyed human entry, got %d", humanEntries)
	}
}

func jsonBody(s string) *strings.Reader { return strings.NewReader(s) }

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// Sustained polling is the expected workload for a diagnosis agent, and the
// usage metadata must not turn it into a write per request. This asserts the
// invariant at the HTTP level, where the throttle actually has to hold.
func TestSustainedPollingDoesNotWritePerRequest(t *testing.T) {
	e := newKeyEnv(t)
	ctx := context.Background()
	key, token, err := e.store.CreateAccessKey(ctx, e.ownerID, "poller",
		[]string{"view"}, []string{e.serverA}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/servers/" + e.serverA
	for i := 0; i < 50; i++ {
		if got := e.do(http.MethodGet, path, token).Code; got != http.StatusOK {
			t.Fatalf("poll %d status=%d want 200", i, got)
		}
	}

	after, err := e.store.GetAccessKey(ctx, key.ID, e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastUsedAt == nil {
		t.Fatal("the first use should still be recorded")
	}
	// 50 requests inside the staleness window from one address: exactly one
	// write, so last_used_at is the first poll's timestamp, not the last one's.
	if after.LastUsedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("last_used_at=%v is in the future", after.LastUsedAt)
	}
	if after.LastUsedIP == nil {
		t.Fatal("the source address should be recorded once")
	}
}

// BenchmarkAuthenticatedServerRead compares the JWT and access-key paths
// through authentication, the machine boundary, and the server access gate:
//
//	go test ./internal/api -run '^$' -bench AuthenticatedServerRead -benchmem
//
// The key path adds one indexed hash lookup on top of the owner row read the
// JWT path already performs, and its usage write is throttled (see
// TestSustainedPollingDoesNotWritePerRequest), so a steady poll should cost
// roughly the same per request. There is no numeric budget in the plan, so this
// exists to record a baseline and catch a material regression later — the
// signal is the gap between the two sub-benchmarks, not the absolute numbers.
//
// The production rate limiter is deliberately left out of the chain: its burst
// allowance would cut a long benchmark short, and it is identical on both paths
// apart from which bucket the request lands in.
func BenchmarkAuthenticatedServerRead(b *testing.B) {
	e := newKeyEnv(b)

	_, token, err := e.store.CreateAccessKey(context.Background(), e.ownerID, "bench",
		[]string{"view"}, []string{e.serverA}, time.Now().Add(time.Hour))
	if err != nil {
		b.Fatal(err)
	}
	jwtTok, err := auth.IssueAccessToken("secret", e.ownerID, "u@example.com", "user")
	if err != nil {
		b.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(auth.Middleware("secret", auth.NewTicketStore(), machineKeyLookup(e.store)))
	r.Use(machineBoundary)
	r.With(requireServerPermission(e.store, store.ServerPermissionView)).
		Get("/api/v1/servers/{id}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	path := "/api/v1/servers/" + e.serverA

	run := func(b *testing.B, bearer string) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+bearer)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent {
				b.Fatalf("status=%d", rr.Code)
			}
		}
	}
	b.Run("jwt", func(b *testing.B) { run(b, jwtTok) })
	b.Run("access-key", func(b *testing.B) { run(b, token) })
}
