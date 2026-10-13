package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// What the agent executes — directory, Java binary, JVM flags — is read only
// by administrators.
func TestServerHostFieldsAreAdminOnly(t *testing.T) {
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Survival")
	h := NewServerHandlers(f.store, t.TempDir())

	get := func(u *store.User, role string) map[string]any {
		rr := httptest.NewRecorder()
		h.Get(rr, withURLParams(as(u, role, http.MethodGet, "/api/v1/servers/"+srv.ID, nil), "id", srv.ID))
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d", rr.Code)
		}
		var out map[string]any
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return out
	}
	mine := get(owner, "user")
	for _, k := range []string{"directory_path", "java_binary", "jvm_args"} {
		if v, ok := mine[k]; !ok || v != nil {
			t.Errorf("owner sees %s = %v, want null", k, v)
		}
	}
	if mine["name"] != "Survival" {
		t.Fatalf("redaction lost the rest of the record: %v", mine)
	}
	full := get(admin, "admin")
	if full["directory_path"] != srv.DirectoryPath || full["java_binary"] != "java" {
		t.Fatalf("admin view: %v", full)
	}
}

// A form that sends the record back with the host fields unchanged is not
// trying to change them, and is not refused for it.
func TestUpdateAcceptsUnchangedHostFields(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Survival")
	h := NewServerHandlers(f.store, t.TempDir())

	update := func(body string) int {
		rr := httptest.NewRecorder()
		h.Update(rr, withURLParams(as(owner, "user", http.MethodPut, "/api/v1/servers/"+srv.ID, strings.NewReader(body)), "id", srv.ID))
		return rr.Code
	}
	if code := update(`{"name":"Renamed","directory_path":"` + srv.DirectoryPath + `"}`); code != http.StatusOK {
		t.Fatalf("unchanged directory_path: status %d", code)
	}
	if code := update(`{"java_binary":"/tmp/evil"}`); code != http.StatusForbidden {
		t.Fatalf("changed java_binary by a non-admin: status %d, want 403", code)
	}
	got, _ := f.store.GetServer(context.Background(), srv.ID)
	if got.Name != "Renamed" || got.JavaBinary != "java" {
		t.Fatalf("record after updates: %+v", got)
	}
}
