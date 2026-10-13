package store

import (
	"context"
	"testing"
)

func TestLogActionReturnsSerializationFailure(t *testing.T) {
	s := testStore(t)
	err := s.LogAction(context.Background(), "", "", "test.invalid", "", make(chan struct{}))
	if err == nil {
		t.Fatal("expected unsupported audit detail to return an error")
	}
}

// Deleting a server must not delete the record of what was done to it, and the
// rows must still say which server they were about.
func TestDeletingAServerKeepsItsAuditHistory(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	node, err := s.CreateNode(ctx, &Node{Name: "n", FQDN: "localhost", Port: 8090, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "h", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &Server{NodeID: node.ID, OwnerID: owner.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"server.start", "backup.restore", "server.delete"} {
		if err := s.LogAction(ctx, owner.ID, srv.ID, action, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}

	entries, err := s.ListAudit(ctx, srv.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("%d audit rows survive the server, want 3", len(entries))
	}
	for _, e := range entries {
		if e.ServerName == nil || *e.ServerName != "survival" {
			t.Fatalf("row %s lost the server's name: %v", e.Action, e.ServerName)
		}
	}
}

// The action filter is a prefix match on user input; LIKE metacharacters in it
// must match literally.
func TestAuditActionPrefixIsLiteral(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, action := range []string{"server_x.start", "serverAx.start", `a\b.c`, "a%b.c"} {
		if err := s.LogAction(ctx, "", "", action, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	for prefix, want := range map[string]int{"server_": 1, "server": 2, `a\`: 1, "a%": 1, "a": 2} {
		entries, _, err := s.ListAuditPage(ctx, AuditQuery{ActionPrefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != want {
			t.Errorf("prefix %q matched %d rows, want %d", prefix, len(entries), want)
		}
	}
}
