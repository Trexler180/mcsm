package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Operations that stop, start, back up or restore a server on the way to their
// goal need every permission they exercise. A collaborator holding only
// "settings" must not be able to borrow power or restore rights through them.
func TestCompoundOperationsNeedEveryPermissionTheyUse(t *testing.T) {
	s, serverID, _, otherID, _ := accessTestStore(t)
	if err := s.SetServerPermissions(context.Background(), serverID, otherID, []string{"settings", "mods.update", "backups.restore"}); err != nil {
		t.Fatal(err)
	}
	secret := "secret"
	r := NewRouter(s, secret, "", nil, testNotifier(s))
	token, err := sessionToken(t, s, secret, otherID, "other@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/reinstall",
		"/migrate",
		"/mods/auto-update",
		"/backups/some-backup/restore",
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+serverID+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 for a collaborator without the power permissions it uses", path, rr.Code)
		}
	}
}
