package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// A console or metrics socket opened with an access key stays open long after
// the request that authenticated it. Its periodic re-check must notice the key
// itself going away, not only its owner's permissions changing — an admin-owned
// key would otherwise keep a live console after being revoked or rotated.
func TestConsolePermissionCheckClosesOnKeyRevocationAndRotation(t *testing.T) {
	ctx := context.Background()
	s := authTestStore(t)
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "admin@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID: node.ID, OwnerID: owner.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}

	// check authenticates token the way the router does and returns the
	// socket's re-check as it would be captured at upgrade time.
	h := NewConsoleHandlers(s)
	check := func(token string) func(context.Context) bool {
		t.Helper()
		lookup := func(ctx context.Context, presented, _ string) (*auth.MachineIdentity, error) {
			res, err := s.AuthenticateAccessKey(ctx, presented)
			if err != nil {
				return nil, err
			}
			return &auth.MachineIdentity{
				KeyID: res.Key.ID, UserID: res.User.ID, Email: res.User.Email, Role: res.User.Role,
				Scopes: res.Key.Scopes, ServerIDs: res.Key.ServerIDs, TokenHash: store.HashAccessKey(presented),
			}, nil
		}
		var captured func(context.Context) bool
		mw := auth.Middleware("secret", auth.NewTicketStore(), lookup)
		r := httptest.NewRequest("GET", "/api/v1/servers/"+srv.ID+"/console", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured = h.permissionCheck(r, srv.ID, store.ServerPermissionConsole)
		})).ServeHTTP(httptest.NewRecorder(), r)
		if captured == nil {
			t.Fatal("the key did not authenticate")
		}
		return captured
	}

	key, token, err := s.CreateAccessKey(ctx, owner.ID, "agent", []string{"console"},
		[]string{srv.ID}, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	original := check(token)
	if !original(ctx) {
		t.Fatal("a live key's socket was closed")
	}

	_, rotated, err := s.RotateAccessKey(ctx, key.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if original(ctx) {
		t.Fatal("a socket opened with the pre-rotation secret stayed open")
	}

	current := check(rotated)
	if !current(ctx) {
		t.Fatal("a socket opened with the rotated secret was closed")
	}
	if err := s.RevokeAccessKey(ctx, key.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if current(ctx) {
		t.Fatal("a socket opened with a revoked key stayed open")
	}
}
