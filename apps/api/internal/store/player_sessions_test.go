package store

import (
	"context"
	"testing"
	"time"
)

func TestPlayerSessionsLifecycle(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	now := time.Now().UTC().Truncate(time.Second)

	// Open two sessions; close one after 30 minutes.
	id1, err := s.StartPlayerSession(ctx, srv.ID, "Alice", "uuid-a", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlayerSession(ctx, srv.ID, "Bob", "", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.EndPlayerSession(ctx, id1, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	open, err := s.OpenPlayerSessions(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].PlayerName != "Bob" {
		t.Fatalf("open sessions = %+v, want just Bob", open)
	}

	// Case-insensitive per-player listing.
	alice, err := s.ListPlayerSessions(ctx, srv.ID, "alice", now.Add(-24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 1 || alice[0].EndedAt == nil {
		t.Fatalf("alice sessions = %+v, want one closed session", alice)
	}

	// Playtime: Alice played 30 min inside the window.
	got := PlaytimeFromSessions(alice, now.Add(-24*time.Hour), now)
	if got < 29*60 || got > 31*60 {
		t.Fatalf("alice playtime = %ds, want ~1800", got)
	}

	// Open sessions count up to "now".
	bob, _ := s.ListPlayerSessions(ctx, srv.ID, "Bob", now.Add(-24*time.Hour), 0)
	bobTime := PlaytimeFromSessions(bob, now.Add(-24*time.Hour), now)
	if bobTime < 59*60 || bobTime > 61*60 {
		t.Fatalf("bob playtime = %ds, want ~3600", bobTime)
	}

	// A window cutoff inside the session clamps the counted start.
	clamped := PlaytimeFromSessions(bob, now.Add(-15*time.Minute), now)
	if clamped < 14*60 || clamped > 16*60 {
		t.Fatalf("clamped playtime = %ds, want ~900", clamped)
	}

	// EndAll closes the stragglers.
	if err := s.EndAllPlayerSessions(ctx, srv.ID, now); err != nil {
		t.Fatal(err)
	}
	open, _ = s.OpenPlayerSessions(ctx, srv.ID)
	if len(open) != 0 {
		t.Fatalf("open after EndAll = %d, want 0", len(open))
	}
}
