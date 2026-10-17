package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func putServer(t *testing.T, f *fixture, actorID, actorRole, serverID, body string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := f.store.GetUserByID(context.Background(), actorID)
	if err != nil {
		t.Fatal(err)
	}
	h := NewServerHandlers(f.store, t.TempDir())
	rr := httptest.NewRecorder()
	h.Update(rr, withURLParams(as(u, actorRole, http.MethodPut, "/", strings.NewReader(body)), "id", serverID))
	return rr
}

// import.jar_file decides which jar the agent runs. A settings collaborator
// re-sending the panel's whole settings object must succeed, but changing that
// key — to point the server at another server's jar, say — must not.
func TestSettingsCollaboratorCannotChangeImport(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Imported")
	srv.Settings = json.RawMessage(`{"import":{"jar_file":"paper.jar","no_install":true}}`)
	if err := f.store.UpdateServer(ctx, srv.ID, srv); err != nil {
		t.Fatal(err)
	}

	resend := `{"settings":{"import":{"jar_file":"paper.jar","no_install":true},"resource_pack":{"enabled":false,"path":"","public_id":"x"}}}`
	if rr := putServer(t, f, owner.ID, "user", srv.ID, resend); rr.Code != http.StatusOK {
		t.Fatalf("re-sending unchanged import with another edit: status %d %s", rr.Code, rr.Body.String())
	}
	hijack := `{"settings":{"import":{"jar_file":"../other/evil.jar","no_install":true}}}`
	if rr := putServer(t, f, owner.ID, "user", srv.ID, hijack); rr.Code != http.StatusForbidden {
		t.Fatalf("changing import as a non-admin: status %d, want 403", rr.Code)
	}
	if rr := putServer(t, f, owner.ID, "user", srv.ID, `{"settings":{"something_new":1}}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("an unknown settings key: status %d, want 400", rr.Code)
	}
	// Omitting import must not drop it either.
	if rr := putServer(t, f, owner.ID, "user", srv.ID, `{"settings":{"helper_mod":false}}`); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	got, _ := f.store.GetServer(ctx, srv.ID)
	if !strings.Contains(string(got.Settings), "paper.jar") {
		t.Fatalf("settings lost the import key: %s", got.Settings)
	}

	admin := f.user("admin@example.com", "admin")
	if rr := putServer(t, f, admin.ID, "admin", srv.ID, hijack); rr.Code != http.StatusBadRequest {
		t.Fatalf("a path as jar_file must be refused even for an admin, got %d", rr.Code)
	}
}

// Changing the version fields installs nothing, so a non-admin is sent to
// Reinstall or Migrate instead of desynchronizing the record from the disk.
func TestVersionFieldsNeedReinstall(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Versioned")
	if rr := putServer(t, f, owner.ID, "user", srv.ID, `{"mc_version":"1.20.1"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("non-admin version change: status %d, want 400", rr.Code)
	}
	if rr := putServer(t, f, owner.ID, "user", srv.ID, `{"mc_version":"1.21.4","name":"Renamed"}`); rr.Code != http.StatusOK {
		t.Fatalf("an unchanged version alongside another edit must pass: %d", rr.Code)
	}
	admin := f.user("admin@example.com", "admin")
	if rr := putServer(t, f, admin.ID, "admin", srv.ID, `{"mc_version":"1.20.1/../../x"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("a malformed version must be refused: %d", rr.Code)
	}
}

func TestServerPortAndRAMValidation(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	a := f.server(owner, "Alpha")
	b := f.server(owner, "Beta")
	if _, err := f.db.Exec(`UPDATE servers SET port = 25566 WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	// Sharing a port is allowed; only running both at once is refused, at start.
	if rr := putServer(t, f, owner.ID, "user", a.ID, `{"port":25566}`); rr.Code != http.StatusOK {
		t.Fatalf("sharing another server's port: status %d, want 200", rr.Code)
	}
	if rr := putServer(t, f, owner.ID, "user", a.ID, `{"port":70000}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range port: status %d, want 400", rr.Code)
	}
	if rr := putServer(t, f, owner.ID, "user", a.ID, `{"ram_mb_min":4096,"ram_mb_max":1024}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("max below min: status %d, want 400", rr.Code)
	}
	mem := 8192
	if _, err := f.db.Exec(`UPDATE nodes SET memory_mb = ? WHERE id = ?`, mem, f.node.ID); err != nil {
		t.Fatal(err)
	}
	if rr := putServer(t, f, owner.ID, "user", a.ID, `{"ram_mb_max":16384}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("more memory than the node has: status %d, want 400", rr.Code)
	}
}

func TestValidateSettingsChangeKeepsProtectedKeys(t *testing.T) {
	cur := json.RawMessage(`{"import":{"jar_file":"a.jar"},"helper_mod":true}`)
	out, err := validateSettingsChange(cur, json.RawMessage(`{"helper_mod":false}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a.jar") {
		t.Fatalf("a protected key was dropped by omission: %s", out)
	}
}
