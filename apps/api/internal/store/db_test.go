package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/migrations"
)

func testStore(t *testing.T) *Store {
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
	return New(db)
}

func TestNodeHeartbeatAndDeleteConflict(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	mem, disk, cpu := 4096, 120, 8
	if err := s.UpdateNodeHeartbeat(ctx, node.ID, &mem, &disk, &cpu); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeen == nil || got.MemoryMb == nil || *got.MemoryMb != mem || got.DiskGb == nil || *got.DiskGb != disk || got.CPUCores == nil || *got.CPUCores != cpu {
		t.Fatalf("heartbeat not persisted: %+v", got)
	}

	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer(ctx, &Server{
		NodeID:        node.ID,
		OwnerID:       user.ID,
		Name:          "survival",
		Platform:      "paper",
		MCVersion:     "1.21.4",
		DirectoryPath: "servers/survival",
		JavaBinary:    "java",
		Port:          25565,
		RAMMbMin:      512,
		RAMMbMax:      2048,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(ctx, node.ID); !errors.Is(err, ErrNodeHasServers) {
		t.Fatalf("DeleteNode error = %v, want ErrNodeHasServers", err)
	}
}
