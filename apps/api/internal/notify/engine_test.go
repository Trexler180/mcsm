package notify

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/notify/channels"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func testStore(t *testing.T) *store.Store {
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
	return store.New(db)
}

// fixture creates a node, an owner with a server, and a second unrelated user.
func fixture(t *testing.T, s *store.Store) (ownerID, otherID, serverID string) {
	t.Helper()
	ctx := context.Background()
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID: node.ID, OwnerID: owner.ID, Name: "survival", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner.ID, other.ID, srv.ID
}

func newTestEngine(s *store.Store) *Engine {
	d := NewDispatcher(s, channels.NewWebhookSender(), nil)
	return NewEngine(s, NewHub(), d)
}

func TestEmitDeliversToSubscriberAndEnqueuesWebhook(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, _, serverID := fixture(t, s)

	ch, err := s.CreateChannel(ctx, &store.NotificationChannel{
		UserID: ownerID, Kind: "webhook", Enabled: true,
		Config: store.NotificationChannelConfig{URL: "https://example.com/hook", Format: "generic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSubscription(ctx, &store.NotificationSubscription{
		UserID: ownerID, EventType: EventServerCrash, MinSeverity: SeverityInfo,
		Channels: []string{"inapp", "webhook:" + ch.ID}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	e := newTestEngine(s)
	if err := e.process(ctx, ServerCrash(serverID, "survival")); err != nil {
		t.Fatal(err)
	}

	feed, err := s.ListNotifications(ctx, ownerID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 {
		t.Fatalf("want 1 feed item, got %d", len(feed))
	}
	if feed[0].EventType != EventServerCrash || feed[0].Severity != SeverityCritical {
		t.Fatalf("unexpected feed item: %+v", feed[0])
	}

	due, err := s.ClaimDueDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].TargetKind != "webhook" || due[0].TargetID != ch.ID {
		t.Fatalf("want 1 webhook delivery for channel, got %+v", due)
	}
}

func TestEmitDedupesWithinWindow(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, _, serverID := fixture(t, s)
	if _, err := s.UpsertSubscription(ctx, &store.NotificationSubscription{
		UserID: ownerID, EventType: EventServerCrash, MinSeverity: SeverityInfo,
		Channels: []string{"inapp"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(s)
	for i := 0; i < 3; i++ {
		if err := e.process(ctx, ServerCrash(serverID, "survival")); err != nil {
			t.Fatal(err)
		}
	}
	feed, _ := s.ListNotifications(ctx, ownerID, false, 50)
	if len(feed) != 1 {
		t.Fatalf("dedupe failed: want 1 feed item, got %d", len(feed))
	}
}

func TestEmitSeverityThresholdFilters(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, _, serverID := fixture(t, s)
	// Only critical: a warning-level "server stopped" must be dropped.
	if _, err := s.UpsertSubscription(ctx, &store.NotificationSubscription{
		UserID: ownerID, EventType: EventServerOffline, MinSeverity: SeverityCritical,
		Channels: []string{"inapp"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(s)
	if err := e.process(ctx, ServerOffline(serverID, "survival")); err != nil {
		t.Fatal(err)
	}
	feed, _ := s.ListNotifications(ctx, ownerID, false, 50)
	if len(feed) != 0 {
		t.Fatalf("severity threshold failed: want 0 feed items, got %d", len(feed))
	}
}

func TestEmitSkipsUserWithoutServerAccess(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, otherID, serverID := fixture(t, s)
	// The non-owner subscribes to all-servers crashes but has no access to this server.
	if _, err := s.UpsertSubscription(ctx, &store.NotificationSubscription{
		UserID: otherID, EventType: EventServerCrash, MinSeverity: SeverityInfo,
		Channels: []string{"inapp"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(s)
	if err := e.process(ctx, ServerCrash(serverID, "survival")); err != nil {
		t.Fatal(err)
	}
	if feed, _ := s.ListNotifications(ctx, otherID, false, 50); len(feed) != 0 {
		t.Fatalf("non-owner must not receive server-scoped alert, got %d", len(feed))
	}
	// Sanity: the owner (with access) still gets nothing here because they have
	// no subscription — confirms the event only routes by subscription.
	if feed, _ := s.ListNotifications(ctx, ownerID, false, 50); len(feed) != 0 {
		t.Fatalf("owner without subscription should get nothing, got %d", len(feed))
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 8; attempt++ {
		d := backoff(attempt)
		if d <= 0 {
			t.Fatalf("backoff(%d) = %v, must be positive", attempt, d)
		}
		if d > backoffCap+backoffCap/5 {
			t.Fatalf("backoff(%d) = %v exceeds cap", attempt, d)
		}
		// Roughly monotonic until the cap (allow jitter slack).
		if attempt > 1 && attempt < 6 && d < prev/2 {
			t.Fatalf("backoff not growing: attempt %d = %v, prev = %v", attempt, d, prev)
		}
		prev = d
	}
}
