package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A non-admin's overview may show what happened on their servers and their own
// panel-wide actions. The global rows — another user's login and IP, a node
// reboot, an integration change — are the admin-only log.
func TestOverviewScopesActivityForNonAdmins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	member := f.user("member@example.com", "user")
	other := f.user("other@example.com", "user")
	mine := f.server(member, "Mine")
	theirs := f.server(other, "Theirs")

	_ = f.store.LogAction(ctx, admin.ID, "", "auth.login", "198.51.100.1", nil)
	_ = f.store.LogAction(ctx, admin.ID, "", "node.reboot", "198.51.100.1", nil)
	_ = f.store.LogAction(ctx, member.ID, "", "auth.login", "198.51.100.2", nil)
	_ = f.store.LogAction(ctx, member.ID, mine.ID, "server.start", "198.51.100.2", nil)
	_ = f.store.LogAction(ctx, other.ID, theirs.ID, "server.start", "198.51.100.3", nil)

	rr := httptest.NewRecorder()
	NewOverviewHandlers(f.store).Overview(rr, as(member, "user", http.MethodGet, "/api/v1/overview", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got overviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Activity) != 2 {
		t.Fatalf("activity has %d rows, want the member's login and their server's start", len(got.Activity))
	}
	for _, e := range got.Activity {
		if e.ServerID == nil && (e.UserID == nil || *e.UserID != member.ID) {
			t.Fatalf("a non-admin saw someone else's global audit row: %s", e.Action)
		}
		if e.ServerID != nil && *e.ServerID != mine.ID {
			t.Fatalf("a non-admin saw activity on a server they cannot access")
		}
	}
}

// The scope applies before the limit: a busy neighbouring server must not push
// a member's own warnings off the end of a global page.
func TestOverviewWarningsScopedBeforeLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	member := f.user("member@example.com", "user")
	other := f.user("other@example.com", "user")
	mine := f.server(member, "Mine")
	theirs := f.server(other, "Theirs")
	_ = f.store.InsertLogEvent(ctx, mine.ID, "error", "mine broke", "poller")
	for i := 0; i < 40; i++ {
		_ = f.store.InsertLogEvent(ctx, theirs.ID, "warn", "noisy", "poller")
	}

	rr := httptest.NewRecorder()
	NewOverviewHandlers(f.store).Overview(rr, as(member, "user", http.MethodGet, "/api/v1/overview", nil))
	var got overviewResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Warnings) != 1 || got.Warnings[0].ServerID != mine.ID {
		t.Fatalf("warnings = %+v, want exactly the member's own event", got.Warnings)
	}
}

// A token minted before a demotion still says "admin" for up to 15 minutes.
// The fleet-wide listing must follow the account, not the token.
func TestServerListFollowsLiveRoleNotToken(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	demoted := f.user("demoted@example.com", "user")
	other := f.user("other@example.com", "user")
	f.server(other, "Someone Else's")

	rr := httptest.NewRecorder()
	NewServerHandlers(f.store, t.TempDir()).List(rr, as(demoted, "admin", http.MethodGet, "/api/v1/servers", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("a stale admin token listed %d servers the account cannot see", len(got))
	}

	rr = httptest.NewRecorder()
	NewOverviewHandlers(f.store).Overview(rr, as(demoted, "admin", http.MethodGet, "/api/v1/overview", nil))
	var ov overviewResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &ov)
	if len(ov.Servers) != 0 {
		t.Fatalf("overview followed the token's role, not the account's")
	}
	_ = ctx
}
