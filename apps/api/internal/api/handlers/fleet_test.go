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
