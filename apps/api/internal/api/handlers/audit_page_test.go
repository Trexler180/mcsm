package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func readAuditPage(t *testing.T, h *AuditHandlers, req *http.Request) auditPage {
	t.Helper()
	rr := httptest.NewRecorder()
	h.Page(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out auditPage
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A user sees rows about their servers and their own actions — never another
// user's server or panel-wide activity — and pages through them by cursor.
func TestAuditPageScopesAndPages(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	alice := f.user("alice@example.com", "user")
	bob := f.user("bob@example.com", "user")
	mine := f.server(alice, "Alice SMP")
	theirs := f.server(bob, "Bob SMP")

	for i := 0; i < 5; i++ {
		_ = f.store.LogAction(ctx, alice.ID, mine.ID, "server.start", "", nil)
	}
	_ = f.store.LogAction(ctx, bob.ID, theirs.ID, "server.start", "", nil)
	_ = f.store.LogAction(ctx, bob.ID, "", "auth.login", "", nil)
	_ = f.store.LogAction(ctx, alice.ID, "", "auth.login", "", nil)

	h := NewAuditHandlers(f.store)

	page := readAuditPage(t, h, as(alice, "user", http.MethodGet, "/api/v1/audit-log?limit=4", nil))
	if len(page.Entries) != 4 || page.NextCursor == 0 {
		t.Fatalf("first page: %d entries, cursor %d", len(page.Entries), page.NextCursor)
	}
	rest := readAuditPage(t, h, as(alice, "user", http.MethodGet,
		"/api/v1/audit-log?limit=4&before="+strconv.FormatInt(page.NextCursor, 10), nil))
	all := append(page.Entries, rest.Entries...)
	if len(all) != 6 || rest.NextCursor != 0 {
		t.Fatalf("alice sees %d rows (cursor %d), want her 6", len(all), rest.NextCursor)
	}
	for _, e := range all {
		if e.UserID == nil || *e.UserID != alice.ID {
			t.Fatalf("alice saw someone else's row: %+v", e.AuditEntry)
		}
	}

	// Asking for another user's server filters to nothing rather than widening.
	other := readAuditPage(t, h, as(alice, "user", http.MethodGet, "/api/v1/audit-log?server_id="+theirs.ID, nil))
	if len(other.Entries) != 0 {
		t.Fatalf("alice read %d rows of bob's server", len(other.Entries))
	}

	// Admins see everything, with emails resolved, and can filter by action.
	logins := readAuditPage(t, h, as(admin, "admin", http.MethodGet, "/api/v1/audit-log?action=auth.", nil))
	if len(logins.Entries) != 2 {
		t.Fatalf("admin saw %d logins, want 2", len(logins.Entries))
	}
	for _, e := range logins.Entries {
		if e.ActorEmail == "" {
			t.Fatal("admin view did not resolve the actor")
		}
	}

	rr := httptest.NewRecorder()
	h.Page(rr, as(admin, "admin", http.MethodGet, "/api/v1/audit-log?since=yesterday", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad since: status %d", rr.Code)
	}
}
