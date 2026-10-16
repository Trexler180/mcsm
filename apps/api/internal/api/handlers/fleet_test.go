package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcsm/api/internal/store"
)

func TestFleetSummary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	alice := f.user("alice@example.com", "user")
	mine := f.server(alice, "Alice SMP")
	other := f.server(admin, "Staff")
	if err := f.store.UpdateServerStatus(ctx, mine.ID, "online"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tps := 19.5
	if err := f.store.InsertServerMetric(ctx, mine.ID, now, 42, 1500, 4096, 3, &tps, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.InsertServerMetric(ctx, mine.ID, now.Add(-3*time.Hour), 10, 900, 4096, 7, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordModUpdateCheck(ctx, mine.ID, 2); err != nil {
		t.Fatal(err)
	}
	task, err := f.store.CreateTask(ctx, &store.ScheduledTask{ServerID: mine.ID, Name: "Nightly restart", CronExpr: "0 4 * * *", Action: "restart", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	next := now.Add(2 * time.Hour)
	if err := f.store.UpdateTaskLastRun(ctx, task.ID, now, &next); err != nil {
		t.Fatal(err)
	}

	h := NewOverviewHandlers(f.store)
	get := func(u *store.User, role string) fleetResponse {
		rr := httptest.NewRecorder()
		h.Fleet(rr, as(u, role, http.MethodGet, "/api/v1/fleet", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		var out fleetResponse
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	got := get(alice, "user")
	if len(got.Servers) != 1 || got.Servers[0].ID != mine.ID {
		t.Fatalf("alice sees %d servers; another user's server leaked: %+v", len(got.Servers), got.Servers)
	}
	s := got.Servers[0]
	if s.Players == nil || *s.Players != 3 || s.TPS == nil || *s.TPS != 19.5 || s.CPUPercent == nil {
		t.Fatalf("live numbers: %+v", s)
	}
	if len(s.Players24h) != 24 || s.Players24h[23] == nil || *s.Players24h[23] != 3 {
		t.Fatalf("this hour's peak missing from the series: %v", s.Players24h)
	}
	var peaks int
	for _, p := range s.Players24h {
		if p != nil {
			peaks++
		}
	}
	if peaks != 2 {
		t.Fatalf("series has %d populated hours, want 2", peaks)
	}
	if !s.BackupOverdue || s.ModUpdates == nil || *s.ModUpdates != 2 {
		t.Fatalf("backup/update flags: %+v", s)
	}
	fig := got.Figures
	if fig.PlayersOnline != 3 || fig.ServersUp != 1 || fig.ServersTotal != 1 || fig.ModUpdates != 2 || fig.BackupsOverdue != 1 {
		t.Fatalf("figures: %+v", fig)
	}
	if len(got.Upcoming) != 1 || got.Upcoming[0].ServerName != "Alice SMP" {
		t.Fatalf("upcoming: %+v", got.Upcoming)
	}

	all := get(admin, "admin")
	if all.Figures.ServersTotal != 2 {
		t.Fatalf("admin sees %d servers", all.Figures.ServersTotal)
	}
	for _, srv := range all.Servers {
		if srv.ID == other.ID && srv.Players != nil {
			t.Fatal("an offline server reported live numbers")
		}
	}
}

// Home's activity says who acted, as far as the viewer may know: an admin
// sees everyone, anyone else sees their own name and nobody else's.
func TestFleetActivityNamesActors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	alice := f.user("alice@example.com", "user")
	bob := f.user("bob@example.com", "user")
	shared := f.server(alice, "Alice SMP")
	if err := f.store.SetServerPermissions(ctx, shared.ID, bob.ID, []string{"view"}); err != nil {
		t.Fatal(err)
	}
	_ = f.store.LogAction(ctx, alice.ID, shared.ID, "server.start", "", nil)
	_ = f.store.LogAction(ctx, bob.ID, shared.ID, "server.stop", "", nil)

	h := NewOverviewHandlers(f.store)
	names := func(u *store.User, role string) map[string]string {
		rr := httptest.NewRecorder()
		h.Fleet(rr, as(u, role, http.MethodGet, "/api/v1/fleet", nil))
		var out struct {
			Activity []struct {
				Action    string `json:"action"`
				ActorName string `json:"actor_name"`
			} `json:"activity"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, e := range out.Activity {
			got[e.Action] = e.ActorName
		}
		return got
	}
	if got := names(alice, "user"); got["server.start"] != "alice@example.com" || got["server.stop"] != "" {
		t.Fatalf("alice's view of who acted: %v", got)
	}
	if got := names(admin, "admin"); got["server.start"] != "alice@example.com" || got["server.stop"] != "bob@example.com" {
		t.Fatalf("admin's view of who acted: %v", got)
	}
}

// Home says when a server's backups are failing, and stops saying so once one
// succeeds again.
func TestFleetReportsFailingBackups(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	srv := f.server(admin, "Survival")
	backup := func(status, reason string, at time.Time) {
		b, err := f.store.CreateBackup(ctx, &store.Backup{ServerID: srv.ID, Trigger: "manual", Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		var size *int64
		if status == "success" {
			n := int64(10)
			size = &n
		}
		if err := f.store.UpdateBackupResult(ctx, b.ID, status, size, reason); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `UPDATE backups SET completed_at = ? WHERE id = ?`, at.UTC().Format("2006-01-02 15:04:05"), b.ID); err != nil {
			t.Fatal(err)
		}
	}
	h := NewOverviewHandlers(f.store)
	server := func() fleetServer {
		rr := httptest.NewRecorder()
		h.Fleet(rr, as(admin, "admin", http.MethodGet, "/api/v1/fleet", nil))
		var out fleetResponse
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Servers[0]
	}
	now := time.Now()
	backup("success", "", now.Add(-3*time.Hour))
	backup("failed", "not enough disk space", now.Add(-2*time.Hour))
	if s := server(); s.BackupFailedAt == nil || s.BackupError != "not enough disk space" || s.LastBackupAt == nil {
		t.Fatalf("failing backup not reported: %+v", s)
	}
	backup("success", "", now.Add(-time.Hour))
	if s := server(); s.BackupFailedAt != nil {
		t.Fatalf("a failure before the latest success is still reported: %+v", s)
	}
}
