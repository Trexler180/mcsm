package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// An update check that finds more updates than the last one raises the
// alert; finding the same ones again does not repeat it.
func TestModUpdatesAlertOnlyWhenThereAreMore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Modded")
	if _, err := f.store.UpsertSubscription(ctx, &store.NotificationSubscription{
		UserID: owner.ID, EventType: notify.EventModUpdateAvailable, MinSeverity: notify.SeverityInfo,
		Channels: []string{"inapp"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	engine := notify.NewEngine(f.store, notify.NewHub(), nil)
	h := NewModHandlers(f.store, t.TempDir(), nil, engine)

	alerts := func() int {
		items, _ := f.store.ListNotifications(ctx, owner.ID, false, 50)
		n := 0
		for _, it := range items {
			if it.EventType == notify.EventModUpdateAvailable {
				n++
			}
		}
		return n
	}
	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for alerts() != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := alerts(); got != want {
			t.Fatalf("%d alerts, want %d", got, want)
		}
	}

	one := []modUpdate{{Name: "Lithium"}}
	two := []modUpdate{{Name: "Lithium"}, {Name: "Sodium"}}
	h.noteUpdates(ctx, srv, one)
	waitFor(1)
	h.noteUpdates(ctx, srv, one)
	time.Sleep(100 * time.Millisecond)
	waitFor(1)
	h.noteUpdates(ctx, srv, two)
	waitFor(2)
	if c, _ := f.store.ModUpdateCheckFor(ctx, srv.ID); c == nil || c.Available != 2 {
		t.Fatalf("last check: %+v", c)
	}
}
