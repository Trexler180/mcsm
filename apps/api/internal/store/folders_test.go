package store

import (
	"context"
	"errors"
	"testing"
)

// folderFixture builds a node, an owner, and a collaborator to hang servers off.
func folderFixture(t *testing.T, s *Store) (ctx context.Context, nodeID, ownerID, otherID string) {
	t.Helper()
	ctx = context.Background()

	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, node.ID, owner.ID, other.ID
}

func mkServer(t *testing.T, s *Store, ctx context.Context, nodeID, ownerID, name string, folderID *string) *Server {
	t.Helper()
	srv, err := s.CreateServer(ctx, &Server{
		NodeID: nodeID, OwnerID: ownerID, Name: name,
		Platform: "paper", MCVersion: "1.21", DirectoryPath: "servers/" + name,
		JavaBinary: "java", Port: 25565, FolderID: folderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestServerFolderCRUDAndCounts(t *testing.T) {
	s := testStore(t)
	ctx, nodeID, ownerID, _ := folderFixture(t, s)

	folder, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Minigames", Color: "blue", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	if folder.ServerCount != 0 {
		t.Fatalf("new folder server_count = %d, want 0", folder.ServerCount)
	}

	mkServer(t, s, ctx, nodeID, ownerID, "bedwars", &folder.ID)
	mkServer(t, s, ctx, nodeID, ownerID, "skywars", &folder.ID)
	mkServer(t, s, ctx, nodeID, ownerID, "survival", nil)

	got, err := s.GetServerFolder(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerCount != 2 {
		t.Fatalf("server_count = %d, want 2", got.ServerCount)
	}

	// The folder id round-trips onto the server itself.
	servers, err := s.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inFolder := 0
	for _, srv := range servers {
		if srv.FolderID != nil && *srv.FolderID == folder.ID {
			inFolder++
		}
	}
	if inFolder != 2 {
		t.Fatalf("%d servers report the folder, want 2", inFolder)
	}

	got.Name = "Mini Games"
	if err := s.UpdateServerFolder(ctx, folder.ID, got); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.GetServerFolder(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Mini Games" {
		t.Fatalf("name = %q, want %q", renamed.Name, "Mini Games")
	}
}

// Deleting a folder is an organizational act: the servers inside it survive and
// fall back to ungrouped.
func TestDeleteServerFolderUngroupsRatherThanDeletes(t *testing.T) {
	s := testStore(t)
	ctx, nodeID, ownerID, _ := folderFixture(t, s)

	folder, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Minigames"})
	if err != nil {
		t.Fatal(err)
	}
	srv := mkServer(t, s, ctx, nodeID, ownerID, "bedwars", &folder.ID)

	if err := s.DeleteServerFolder(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}

	survivor, err := s.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("server should outlive its folder: %v", err)
	}
	if survivor.FolderID != nil {
		t.Fatalf("folder_id = %v, want nil after the folder was deleted", *survivor.FolderID)
	}
	if err := s.DeleteServerFolder(ctx, folder.ID); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("second delete = %v, want ErrFolderNotFound", err)
	}
}

// Names are the handle people use to tell folders apart, so a case-only variant
// is a collision, not a new folder.
func TestServerFolderNamesAreUniqueCaseInsensitively(t *testing.T) {
	s := testStore(t)
	ctx, _, _, _ := folderFixture(t, s)

	first, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Minigames"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "minigames"}); !errors.Is(err, ErrFolderNameTaken) {
		t.Fatalf("duplicate create = %v, want ErrFolderNameTaken", err)
	}

	second, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Staging"})
	if err != nil {
		t.Fatal(err)
	}
	second.Name = "MINIGAMES"
	if err := s.UpdateServerFolder(ctx, second.ID, second); !errors.Is(err, ErrFolderNameTaken) {
		t.Fatalf("rename onto an existing name = %v, want ErrFolderNameTaken", err)
	}
	// The original is untouched by the rejected rename.
	if got, err := s.GetServerFolder(ctx, first.ID); err != nil || got.Name != "Minigames" {
		t.Fatalf("original folder = %+v (err %v)", got, err)
	}
}

// A non-admin must not learn that a folder exists through servers they can't
// open, and the count they see must only cover servers they can.
func TestListServerFoldersForUserScopesToVisibleServers(t *testing.T) {
	s := testStore(t)
	ctx, nodeID, ownerID, otherID := folderFixture(t, s)

	shared, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Minigames"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Empty"})
	if err != nil {
		t.Fatal(err)
	}

	visible := mkServer(t, s, ctx, nodeID, ownerID, "bedwars", &shared.ID)
	mkServer(t, s, ctx, nodeID, ownerID, "skywars", &shared.ID) // same folder, not shared
	mkServer(t, s, ctx, nodeID, ownerID, "secret", &hidden.ID)

	if err := s.SetServerPermissions(ctx, visible.ID, otherID, []string{string(ServerPermissionView)}); err != nil {
		t.Fatal(err)
	}

	folders, err := s.ListServerFoldersForUser(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 {
		names := []string{}
		for _, f := range folders {
			names = append(names, f.Name)
		}
		t.Fatalf("collaborator sees folders %v, want only [Minigames]", names)
	}
	if folders[0].ID != shared.ID {
		t.Fatalf("visible folder = %q, want Minigames", folders[0].Name)
	}
	if folders[0].ServerCount != 1 {
		t.Fatalf("server_count = %d, want 1 — the unshared sibling must not be counted", folders[0].ServerCount)
	}

	// Admins keep the full picture, empty folders included.
	all, err := s.ListServerFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("admin sees %d folders, want 3", len(all))
	}
	byID := map[string]*ServerFolder{}
	for _, f := range all {
		byID[f.ID] = f
	}
	if byID[shared.ID].ServerCount != 2 {
		t.Fatalf("admin count for Minigames = %d, want 2", byID[shared.ID].ServerCount)
	}
	if byID[empty.ID].ServerCount != 0 {
		t.Fatalf("admin count for Empty = %d, want 0", byID[empty.ID].ServerCount)
	}
}

// Listings drive the rendered order directly, so sort_order wins and equal
// weights fall back to name rather than insertion order.
func TestListServerFoldersOrdersBySortThenName(t *testing.T) {
	s := testStore(t)
	ctx, _, _, _ := folderFixture(t, s)

	for _, f := range []ServerFolder{
		{Name: "zulu", SortOrder: 0},
		{Name: "alpha", SortOrder: 0},
		{Name: "pinned", SortOrder: -1},
	} {
		if _, err := s.CreateServerFolder(ctx, &f); err != nil {
			t.Fatal(err)
		}
	}

	folders, err := s.ListServerFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pinned", "alpha", "zulu"}
	for i, name := range want {
		if folders[i].Name != name {
			got := []string{}
			for _, f := range folders {
				got = append(got, f.Name)
			}
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// SetServerFolder is the move primitive: into a folder, and back out again.
func TestSetServerFolderMovesAndClears(t *testing.T) {
	s := testStore(t)
	ctx, nodeID, ownerID, _ := folderFixture(t, s)

	folder, err := s.CreateServerFolder(ctx, &ServerFolder{Name: "Minigames"})
	if err != nil {
		t.Fatal(err)
	}
	srv := mkServer(t, s, ctx, nodeID, ownerID, "bedwars", nil)

	if err := s.SetServerFolder(ctx, srv.ID, &folder.ID); err != nil {
		t.Fatal(err)
	}
	moved, err := s.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.FolderID == nil || *moved.FolderID != folder.ID {
		t.Fatalf("folder_id = %v, want %q", moved.FolderID, folder.ID)
	}

	if err := s.SetServerFolder(ctx, srv.ID, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.FolderID != nil {
		t.Fatalf("folder_id = %v, want nil", *cleared.FolderID)
	}
}
