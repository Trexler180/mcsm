package store

import (
	"context"
	"testing"
	"time"
)

func uptimeTestServer(t *testing.T, s *Store) *Server {
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
		NodeID: node.ID, OwnerID: user.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func countUptimeSegments(t *testing.T, s *Store, serverID string) (total, open int) {
	t.Helper()
	err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN ended_at IS NULL THEN 1 ELSE 0 END), 0)
		 FROM server_uptime WHERE server_id = ?`, serverID,
	).Scan(&total, &open)
	if err != nil {
		t.Fatal(err)
	}
	return total, open
}

func TestUpdateServerStatusMaintainsUptimeSegments(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := uptimeTestServer(t, s)

	// starting → no segment yet
	if err := s.UpdateServerStatus(ctx, srv.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	if total, _ := countUptimeSegments(t, s, srv.ID); total != 0 {
		t.Fatalf("segment before online: %d", total)
	}

	// online → open segment, surfaced as OnlineSince on the model
	if err := s.UpdateServerStatus(ctx, srv.ID, "online"); err != nil {
		t.Fatal(err)
	}
	if total, open := countUptimeSegments(t, s, srv.ID); total != 1 || open != 1 {
		t.Fatalf("after online: total=%d open=%d", total, open)
	}
	got, err := s.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OnlineSince == nil {
		t.Fatal("OnlineSince not set for online server")
	}

	// same-status write must not churn segments
	if err := s.UpdateServerStatus(ctx, srv.ID, "online"); err != nil {
		t.Fatal(err)
	}
	if total, open := countUptimeSegments(t, s, srv.ID); total != 1 || open != 1 {
		t.Fatalf("after repeat online: total=%d open=%d", total, open)
	}

	// clean stop closes with reason "stop"
	if err := s.UpdateServerStatus(ctx, srv.ID, "stopping"); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := s.db.QueryRow(
		`SELECT end_reason FROM server_uptime WHERE server_id = ? ORDER BY id DESC LIMIT 1`, srv.ID,
	).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "stop" {
		t.Fatalf("end_reason = %q, want stop", reason)
	}
	if _, open := countUptimeSegments(t, s, srv.ID); open != 0 {
		t.Fatal("segment still open after stop")
	}
	got, err = s.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OnlineSince != nil {
		t.Fatal("OnlineSince set for stopped server")
	}

	// crash path stamps "crash"
	if err := s.UpdateServerStatus(ctx, srv.ID, "online"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServerStatusCrash(ctx, srv.ID, "offline"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(
		`SELECT end_reason FROM server_uptime WHERE server_id = ? ORDER BY id DESC LIMIT 1`, srv.ID,
	).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "crash" {
		t.Fatalf("end_reason = %q, want crash", reason)
	}
	if total, open := countUptimeSegments(t, s, srv.ID); total != 2 || open != 0 {
		t.Fatalf("after crash: total=%d open=%d", total, open)
	}
}

func TestUptimeReportMath(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := uptimeTestServer(t, s)

	now := time.Now().Truncate(time.Second)
	nowU := now.Unix()
	insert := func(start, end int64, reason string) {
		t.Helper()
		var endV any
		if end != 0 {
			endV = end
		}
		if _, err := s.db.Exec(
			`INSERT INTO server_uptime (server_id, started_at, ended_at, end_reason) VALUES (?,?,?,?)`,
			srv.ID, start, endV, reason,
		); err != nil {
			t.Fatal(err)
		}
	}

	// Timeline (hours before now):  [-30h ── -25h] crash   [-20h ── -10h] stop   [-2h ── open)
	h := int64(3600)
	insert(nowU-30*h, nowU-25*h, "crash")
	insert(nowU-20*h, nowU-10*h, "stop")
	insert(nowU-2*h, 0, "")

	// 24h window: first segment ends outside (excluded), second lies fully
	// inside, open segment counts up to now.
	since := now.Add(-24 * time.Hour)
	r, err := s.UptimeReport(ctx, srv.ID, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Segments) != 2 {
		t.Fatalf("segments in 24h window = %d, want 2", len(r.Segments))
	}
	wantUp := 10*h + 2*h // (-20h..-10h) + (-2h..now)
	if r.UptimeSeconds != wantUp {
		t.Fatalf("uptime = %d, want %d", r.UptimeSeconds, wantUp)
	}
	if r.Stops != 1 || r.Crashes != 0 {
		t.Fatalf("stops=%d crashes=%d, want 1/0", r.Stops, r.Crashes)
	}
	if r.OnlineSince != nowU-2*h {
		t.Fatalf("online_since = %d, want %d", r.OnlineSince, nowU-2*h)
	}
	// The straddling segment's real duration (10h) beats the open one's 2h.
	if r.LongestSeconds != 10*h {
		t.Fatalf("longest = %d, want %d", r.LongestSeconds, 10*h)
	}
	if r.TrackedSince != nowU-30*h {
		t.Fatalf("tracked_since = %d, want %d", r.TrackedSince, nowU-30*h)
	}
	if r.WindowSeconds != 24*h {
		t.Fatalf("window = %d, want %d", r.WindowSeconds, 24*h)
	}
	wantPct := float64(wantUp) / float64(24*h) * 100
	if diff := r.AvailabilityPct - wantPct; diff > 0.01 || diff < -0.01 {
		t.Fatalf("availability = %f, want %f", r.AvailabilityPct, wantPct)
	}

	// 7d window: tracking began 30h ago, so the availability denominator clips
	// to tracked_since instead of billing 7 days of untracked history.
	r, err = s.UptimeReport(ctx, srv.ID, now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if r.WindowSeconds != 30*h {
		t.Fatalf("clipped window = %d, want %d", r.WindowSeconds, 30*h)
	}
	if r.UptimeSeconds != 17*h { // 5h + 10h + 2h
		t.Fatalf("7d uptime = %d, want %d", r.UptimeSeconds, 17*h)
	}
	if r.Stops != 1 || r.Crashes != 1 {
		t.Fatalf("7d stops=%d crashes=%d, want 1/1", r.Stops, r.Crashes)
	}

	// Untracked server: zero-value report, no error.
	other := &Server{
		NodeID: srv.NodeID, OwnerID: srv.OwnerID, Name: "creative", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/creative", JavaBinary: "java",
		Port: 25566, RAMMbMin: 512, RAMMbMax: 2048,
	}
	created, err := s.CreateServer(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.UptimeReport(ctx, created.ID, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.TrackedSince != 0 || r.WindowSeconds != 0 || len(r.Segments) != 0 {
		t.Fatalf("untracked server report not empty: %+v", r)
	}
}
