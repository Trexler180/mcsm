package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func accessTestStore(t *testing.T) (*store.Store, string, string, string) {
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
	return s, srv.ID, owner.ID, other.ID
}

func TestRequireServerAccessAllowsAdminAndOwnerOnly(t *testing.T) {
	s, serverID, ownerID, otherID := accessTestStore(t)
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
		{"admin", "admin-1", "admin", http.StatusOK},
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
