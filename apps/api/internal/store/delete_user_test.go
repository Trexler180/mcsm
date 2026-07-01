package store

import (
	"context"
	"errors"
	"testing"
)

// TestDeleteUserWithReferences reproduces the bug where deleting a user failed
// with "FOREIGN KEY constraint failed (787)" because rows in audit_log, backups
// and scheduled_tasks referenced the user with no ON DELETE action. Migration
// 014 fixes the FKs (SET NULL for the historical rows, CASCADE for tasks so an
// orphaned task can't keep running unauthorized). This user owns no servers.
func TestDeleteUserWithReferences(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// The user we delete: a limited collaborator on someone else's server.
	helper, err := s.CreateUser(ctx, "helper@example.com", "hash", "user")
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

	// Grant access (CASCADE), log an action (SET NULL), trigger a backup
	// (SET NULL) and create a scheduled task (CASCADE) — all as the helper.
	if err := s.SetServerPermissions(ctx, srv.ID, helper.ID, []string{"view"}); err != nil {
		t.Fatal(err)
	}
	s.LogAction(ctx, helper.ID, srv.ID, "server.view", "127.0.0.1", nil)
	backup, err := s.CreateBackup(ctx, &Backup{ServerID: srv.ID, TriggeredBy: &helper.ID, Trigger: "manual", Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, &ScheduledTask{ServerID: srv.ID, Name: "nightly", CronExpr: "0 3 * * *", Action: "backup", CreatedBy: &helper.ID})
	if err != nil {
		t.Fatal(err)
	}

	// The bug: this used to fail with FOREIGN KEY constraint failed (787).
	if err := s.DeleteUser(ctx, helper.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// audit_log + backups: row survives, user link is nulled.
	gotBackup, err := s.GetBackup(ctx, backup.ID)
	if err != nil {
		t.Fatalf("GetBackup: %v", err)
	}
	if gotBackup.TriggeredBy != nil {
		t.Fatalf("backup.triggered_by = %v, want nil after user delete", *gotBackup.TriggeredBy)
	}
	var auditUser *string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM audit_log WHERE server_id = ?`, srv.ID).Scan(&auditUser); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if auditUser != nil {
		t.Fatalf("audit_log.user_id = %v, want nil after user delete", *auditUser)
	}

	// scheduled_tasks: orphaned task is deleted (must not outlive its creator).
	if _, err := s.GetTask(ctx, task.ID); err == nil {
		t.Fatal("expected task to be deleted after its creator was removed")
	}
}

// TestDeleteUserOwningServersBlocked verifies the guard that refuses to delete a
// user who still owns servers, rather than orphaning or cascade-deleting them.
func TestDeleteUserOwningServersBlocked(t *testing.T) {
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
	if _, err := s.CreateServer(ctx, &Server{
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
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteUser(ctx, owner.ID); !errors.Is(err, ErrUserOwnsServers) {
		t.Fatalf("DeleteUser error = %v, want ErrUserOwnsServers", err)
	}
}
