package lifecycle

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// recordingAgent is a node whose server is online until it is stopped.
type recordingAgent struct {
	mu      sync.Mutex
	calls   []string
	stopped bool
	purge   error
}

func (a *recordingAgent) record(c string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, c)
}

func (a *recordingAgent) seen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.calls, ",")
}

func (a *recordingAgent) RegisterDir(context.Context, string, string) error { return nil }
func (a *recordingAgent) StartServer(context.Context, string, map[string]any) error {
	a.record("start")
	return nil
}
func (a *recordingAgent) StopServer(context.Context, string, bool, int) error {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
	a.record("stop")
	return nil
}
func (a *recordingAgent) RestartServer(context.Context, string, map[string]any) error { return nil }
func (a *recordingAgent) KillServer(context.Context, string) error                    { return nil }
func (a *recordingAgent) Reinstall(context.Context, string, map[string]any) error     { return nil }
func (a *recordingAgent) GetStatus(context.Context, string) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return map[string]any{"status": "offline"}, nil
	}
	return map[string]any{"status": "online"}, nil
}
func (a *recordingAgent) PurgeServer(context.Context, string, string, bool, bool) error {
	a.record("purge")
	return a.purge
}

func testService(t *testing.T, a Agent) (*Service, *store.Store, *store.Server) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_time_format=datetime&_timezone=UTC")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	_ = goose.SetDialect("sqlite3")
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	s := store.New(db)
	ctx := context.Background()
	node, _ := s.CreateNode(ctx, &store.Node{Name: "n", FQDN: "localhost", Port: 1, Scheme: "http"}, "t")
	owner, _ := s.CreateUser(ctx, "owner@example.com", "h", "user")
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "Doomed", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/doomed", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	return New(s, operations.New(s, bus), bus, func(context.Context, *store.Server) (Agent, error) { return a, nil }), s, srv
}

// Deleting a running server stops it (so the world is saved) before the node
// forgets it, and the record of the deletion survives the server.
func TestDeleteStopsTheServerFirst(t *testing.T) {
	a := &recordingAgent{}
	life, s, srv := testService(t, a)
	ctx := context.Background()

	if err := life.Delete(ctx, srv, operations.System, DeleteOptions{Files: true}); err != nil {
		t.Fatal(err)
	}
	if got := a.seen(); got != "stop,purge" {
		t.Fatalf("node calls %q, want the stop before the purge", got)
	}
	if _, err := s.GetServer(ctx, srv.ID); err == nil {
		t.Fatal("the server record survived its deletion")
	}
	entries, err := s.ListAudit(ctx, srv.ID, 10)
	if err != nil || len(entries) == 0 || entries[0].Action != "server.delete" {
		t.Fatalf("deletion not in the server's history: %v %v", entries, err)
	}
	if entries[0].ServerName == nil || *entries[0].ServerName != "Doomed" {
		t.Fatal("the deletion record lost the server's name")
	}
}

// A node that cannot purge the server leaves it in place unless forced.
func TestDeleteKeepsTheServerWhenTheNodeFails(t *testing.T) {
	a := &recordingAgent{purge: context.DeadlineExceeded}
	life, s, srv := testService(t, a)
	ctx := context.Background()

	if err := life.Delete(ctx, srv, operations.System, DeleteOptions{}); err == nil {
		t.Fatal("a failed purge reported success")
	}
	if _, err := s.GetServer(ctx, srv.ID); err != nil {
		t.Fatal("the server was deleted although the node kept it")
	}
	if err := life.Delete(ctx, srv, operations.System, DeleteOptions{Force: true}); err != nil {
		t.Fatalf("forced delete: %v", err)
	}
	if _, err := s.GetServer(ctx, srv.ID); err == nil {
		t.Fatal("a forced delete left the server")
	}
}

// A delete refuses while an operation holds the server, unless forced.
func TestDeleteRespectsTheOperationLock(t *testing.T) {
	a := &recordingAgent{}
	life, s, srv := testService(t, a)
	ctx := context.Background()
	if err := s.CreateOperation(ctx, &store.Operation{ServerID: srv.ID, Kind: "restore", Exclusive: true, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	err := life.Delete(ctx, srv, operations.System, DeleteOptions{})
	if _, busy := store.AsServerBusy(err); !busy {
		t.Fatalf("err = %v, want busy", err)
	}
	if a.seen() != "" {
		t.Fatal("the node was called while the server was busy")
	}
}
