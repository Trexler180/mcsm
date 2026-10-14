package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTypedSettings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "SMP")
	srv.Settings = json.RawMessage(`{"import":{"jar_file":"server.jar","no_install":true}}`)
	if err := f.store.UpdateServer(ctx, srv.ID, srv); err != nil {
		t.Fatal(err)
	}
	h := NewServerHandlers(f.store, t.TempDir())

	patch := func(body string) (int, map[string]any) {
		rr := httptest.NewRecorder()
		h.PatchSettings(rr, withURLParams(as(owner, "user", http.MethodPatch, "/", strings.NewReader(body)), "id", srv.ID))
		var out map[string]any
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return rr.Code, out
	}

	code, out := patch(`{"helper_mod":true,"connect_address":"play.example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("patch: %d %v", code, out)
	}
	values := out["values"].(map[string]any)
	if values["helper_mod"] != true || values["connect_address"] != "play.example.com" {
		t.Fatalf("values: %v", values)
	}
	if _, ok := values["import"]; ok {
		t.Fatal("a non-admin was shown the import settings")
	}
	got, _ := f.store.GetServer(ctx, srv.ID)
	if !strings.Contains(string(got.Settings), `"jar_file":"server.jar"`) {
		t.Fatalf("a patch dropped a key it didn't name: %s", got.Settings)
	}

	if code, _ := patch(`{"import":null}`); code != http.StatusBadRequest {
		t.Fatalf("a non-admin removed the import settings: %d", code)
	}
	if code, _ := patch(`{"helper_mod":"yes"}`); code != http.StatusBadRequest {
		t.Fatalf("a mistyped value was accepted: %d", code)
	}
	if code, _ := patch(`{"no_such_key":1}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown key was accepted: %d", code)
	}
}
