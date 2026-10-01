package api

import (
	"context"
	"net/http"
	"testing"
)

// Rebooting a host is never reachable by automation, whoever owns the
// credential, and never by a non-admin. The handler's own step-up is tested
// alongside it; this pins the router gates in front of it.
func TestNodeRebootIsAdminAndHumanOnly(t *testing.T) {
	e := newKeyEnv(t)
	nodes, err := e.store.ListNodes(context.Background())
	if err != nil || len(nodes) == 0 {
		t.Fatalf("no node in fixture: %v", err)
	}
	path := "/api/v1/nodes/" + nodes[0].ID + "/reboot"

	// An admin-owned access key: admin ownership must not open it.
	key := e.issueKey(t, e.adminID, []string{"view", "power"}, []string{e.serverA})
	if got := e.do(http.MethodPost, path, key).Code; got != http.StatusForbidden {
		t.Fatalf("admin-owned access key: status=%d, want 403", got)
	}
	// A signed-in non-admin.
	if got := e.do(http.MethodPost, path, e.jwtToken(e.ownerID, "user")).Code; got != http.StatusForbidden {
		t.Fatalf("non-admin session: status=%d, want 403", got)
	}
	// A signed-in admin reaches the handler, which then demands the step-up.
	if got := e.do(http.MethodPost, path, e.jwtToken(e.adminID, "admin")).Code; got != http.StatusBadRequest && got != http.StatusUnauthorized {
		t.Fatalf("admin session without a step-up: status=%d, want 400/401 from the handler", got)
	}
}
