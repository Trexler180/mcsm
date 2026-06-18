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

func TestServerPermissionHierarchy(t *testing.T) {
	// A group present alongside its own leaves collapses to just the group.
	got, err := NormalizeServerPermissions([]string{"power.start", "power", "files.read", "files.read"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"files.read", "power"}; !samePermissionSet(got, want) {
		t.Fatalf("normalize=%v want %v", got, want)
	}

	// A leaf grants itself and (because any grant implies view) view — but not
	// sibling leaves and not the whole group.
	leaf := []string{"power.start"}
	if !HasServerPermission(leaf, ServerPermissionPowerStart) {
		t.Fatal("power.start should grant power.start")
	}
	if HasServerPermission(leaf, ServerPermissionPowerStop) {
		t.Fatal("power.start must not grant power.stop")
	}
	if HasServerPermission(leaf, ServerPermissionPower) {
		t.Fatal("a leaf must not grant the whole group")
	}
	if !HasServerPermission(leaf, ServerPermissionView) {
		t.Fatal("any grant should imply view")
	}

	// A group grants every leaf beneath it.
	if !HasServerPermission([]string{"power"}, ServerPermissionPowerKill) {
		t.Fatal("power should imply power.kill")
	}

	// Group (read) access is granted by holding any leaf of the group, but a
	// leaf must not grant a sibling mutation, and an unrelated perm grants none.
	if !HasServerGroupAccess([]string{"files.read"}, ServerPermissionFiles) {
		t.Fatal("files.read should grant files read access")
	}
	if HasServerPermission([]string{"files.read"}, ServerPermissionFilesWrite) {
		t.Fatal("files.read must not grant files.write")
	}
	if HasServerGroupAccess([]string{"view"}, ServerPermissionFiles) {
		t.Fatal("view must not grant files access")
	}

	// admin implies everything.
	if !HasServerPermission([]string{"admin"}, ServerPermissionBackupsDelete) {
		t.Fatal("admin should imply backups.delete")
	}
}

func TestServerPermissionsStoreBehavior(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	helper, err := s.CreateUser(ctx, "Helper@Example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &Server{
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

	if _, err := s.GetUserByEmailInsensitive(ctx, "helper@example.com"); err != nil {
		t.Fatalf("case-insensitive email lookup failed: %v", err)
	}
	if _, err := NormalizeServerPermissions([]string{"view", "bad"}); !errors.Is(err, ErrInvalidServerPermission) {
		t.Fatalf("NormalizeServerPermissions error=%v, want ErrInvalidServerPermission", err)
	}
	if err := s.SetServerPermissions(ctx, srv.ID, helper.ID, []string{"players", "view", "players"}); err != nil {
		t.Fatal(err)
	}
	perms, ok, err := s.GetServerPermissions(ctx, srv.ID, helper.ID)
	if err != nil || !ok {
		t.Fatalf("GetServerPermissions ok=%v err=%v", ok, err)
	}
	if got := len(perms); got != 2 {
		t.Fatalf("permissions=%v, want de-duped two values", perms)
	}

	canView, err := s.UserHasServerPermission(ctx, helper.ID, srv.ID, ServerPermissionView)
	if err != nil || !canView {
		t.Fatalf("helper view access=%v err=%v", canView, err)
	}
	canPower, err := s.UserHasServerPermission(ctx, helper.ID, srv.ID, ServerPermissionPower)
	if err != nil || canPower {
		t.Fatalf("helper power access=%v err=%v, want false", canPower, err)
	}
	servers, err := s.ListServersForUser(ctx, helper.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != srv.ID {
		t.Fatalf("shared servers=%v, want only %s", servers, srv.ID)
	}

	if err := s.SetServerPermissionsIfCurrent(ctx, srv.ID, helper.ID, []string{"view", "power"}, []string{"view"}); !errors.Is(err, ErrServerPermissionsStale) {
		t.Fatalf("stale update error=%v, want ErrServerPermissionsStale", err)
	}
	if err := s.SetServerPermissionsIfCurrent(ctx, srv.ID, helper.ID, []string{"view", "power"}, []string{"players", "view"}); err != nil {
		t.Fatalf("valid update failed: %v", err)
	}
	canPower, err = s.UserHasServerPermission(ctx, helper.ID, srv.ID, ServerPermissionPower)
	if err != nil || !canPower {
		t.Fatalf("helper power access after update=%v err=%v", canPower, err)
	}
	if err := s.DeleteServerPermissions(ctx, srv.ID, helper.ID); err != nil {
		t.Fatal(err)
	}
	canView, err = s.UserHasServerPermission(ctx, helper.ID, srv.ID, ServerPermissionView)
	if err != nil || canView {
		t.Fatalf("helper view access after delete=%v err=%v, want false", canView, err)
	}
}
