package handlers

import (
	"context"
	"database/sql"
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

// newTestStore opens a private in-memory database with every migration
// applied.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
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
	return store.New(db).WithEncryption("test-master-key")
}

type fixture struct {
	t     *testing.T
	store *store.Store
	node  *store.Node
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := newTestStore(t)
	node, err := s.CreateNode(context.Background(), &store.Node{Name: "local", FQDN: "127.0.0.1", Port: 1, Scheme: "http"}, "node-secret")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, store: s, node: node}
}

func (f *fixture) user(email, role string) *store.User {
	f.t.Helper()
	u, err := f.store.CreateUser(context.Background(), email, mustHash(f.t, "correct horse battery"), role)
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f *fixture) server(owner *store.User, name string) *store.Server {
	f.t.Helper()
	srv, err := f.store.CreateServer(context.Background(), &store.Server{
		NodeID: f.node.ID, OwnerID: owner.ID, Name: name, Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return srv
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// as builds a request carrying claims for u. role is what the token claims,
// which tests use to model a token issued before a role change.
func as(u *store.User, role, method, target string, body *strings.Reader) *http.Request {
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", "application/json")
	}
	return req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{UserID: u.ID, Email: u.Email, Role: role}))
}

// withURLParams adds chi route parameters to a request built outside a router.
func withURLParams(r *http.Request, kv ...string) *http.Request {
	rc := chi.NewRouteContext()
	for i := 0; i+1 < len(kv); i += 2 {
		rc.URLParams.Add(kv[i], kv[i+1])
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}
