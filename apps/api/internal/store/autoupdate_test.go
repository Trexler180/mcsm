package store

import (
	"context"
	"encoding/json"
	"testing"
)

// seedServer creates the node/user/server rows the FK constraints need.
func seedServer(t *testing.T, s *Store) *Server {
	t.Helper()
	ctx := context.Background()
	node, err := s.CreateNode(ctx, &Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &Server{
		NodeID:        node.ID,
		OwnerID:       user.ID,
		Name:          "smp",
		Platform:      "fabric",
		MCVersion:     "1.21.4",
		DirectoryPath: "servers/smp",
		JavaBinary:    "java",
		Port:          25565,
		RAMMbMin:      512,
		RAMMbMax:      2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestSkippedModVersionsCRUD(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	v := &SkippedModVersion{
		ServerID:  srv.ID,
		ProjectID: "projA",
		VersionID: "vA2",
		ModName:   "Mod A",
		Version:   "2.0",
		Reason:    "server crashed during startup",
	}
	if err := s.AddSkippedModVersion(ctx, v); err != nil {
		t.Fatal(err)
	}
	// Re-adding the same version updates the reason instead of erroring.
	v.Reason = "mod conflict: requires libX 2.0"
	if err := s.AddSkippedModVersion(ctx, v); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := s.ListSkippedModVersions(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 row, got %d", len(list))
	}
	got := list[0]
	if got.ProjectID != "projA" || got.VersionID != "vA2" || got.Reason != "mod conflict: requires libX 2.0" || got.CreatedAt.IsZero() {
		t.Fatalf("wrong row: %+v", got)
	}

	if err := s.DeleteSkippedModVersion(ctx, srv.ID, "projA", "vA2"); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListSkippedModVersions(ctx, srv.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("want empty list after delete, got %v (%v)", list, err)
	}
}

func TestModUpdateRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	detail := json.RawMessage(`{"phase":"checking","mods":[]}`)
	run, err := s.CreateModUpdateRun(ctx, srv.ID, "manual", detail)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" || run.FinishedAt != nil || run.Trigger != "manual" {
		t.Fatalf("fresh run: %+v", run)
	}

	// Progress write: status stays running, no finished_at.
	mid := json.RawMessage(`{"phase":"verifying","mods":[]}`)
	if err := s.UpdateModUpdateRun(ctx, run.ID, "running", mid, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModUpdateRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinishedAt != nil || string(got.Detail) != string(mid) {
		t.Fatalf("mid-run row: %+v", got)
	}

	// Terminal write stamps finished_at.
	done := json.RawMessage(`{"phase":"done","mods":[]}`)
	if err := s.UpdateModUpdateRun(ctx, run.ID, "success", done, true); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetModUpdateRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || got.FinishedAt == nil {
		t.Fatalf("terminal row: %+v", got)
	}

	runs, err := s.ListModUpdateRuns(ctx, srv.ID, 0)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("list runs: %v (%v)", runs, err)
	}

	if _, err := s.GetModUpdateRun(ctx, "missing"); err == nil {
		t.Fatal("want error for unknown run id")
	}
}
