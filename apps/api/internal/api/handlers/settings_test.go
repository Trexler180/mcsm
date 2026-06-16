package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

const settingsTestSecret = "settings-test-secret"

func settingsTestStore(t *testing.T) *store.Store {
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
	return store.New(db).WithEncryption("master")
}

// settingsRouter mounts the integration routes under the same auth + AdminOnly
// stack the real router uses, so tests exercise authorization too.
func settingsRouter(s *store.Store) http.Handler {
	h := NewSettingsHandlers(s)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(settingsTestSecret, auth.NewTicketStore()))
		r.Route("/settings/integrations", func(r chi.Router) {
			r.Use(auth.AdminOnly)
			r.Get("/", h.ListIntegrations)
			r.Put("/{key}", h.SetIntegration)
			r.Delete("/{key}", h.DeleteIntegration)
		})
	})
	return r
}

func adminToken(t *testing.T) string {
	t.Helper()
	tok, err := auth.IssueAccessToken(settingsTestSecret, "u1", "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestIntegrationsRequireAdmin(t *testing.T) {
	r := settingsRouter(settingsTestStore(t))

	// No token at all → 401.
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/settings/integrations/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status=%d, want 401", rr.Code)
	}

	// A non-admin token → 403.
	userTok, err := auth.IssueAccessToken(settingsTestSecret, "u2", "user@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings/integrations/", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("user token: status=%d, want 403", rr.Code)
	}
}

func TestSetIntegrationRejectsUnknownKey(t *testing.T) {
	r := settingsRouter(settingsTestStore(t))
	body, _ := json.Marshal(map[string]string{"value": "whatever"})
	req := httptest.NewRequest(http.MethodPut, "/settings/integrations/not_a_real_key", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(t))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for unknown key", rr.Code)
	}
}

func TestSetListDeleteIntegration(t *testing.T) {
	// Stub CurseForge validation so the save path doesn't hit the network.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("validation request missing x-api-key")
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer stub.Close()
	orig := curseforgeVersionURL
	curseforgeVersionURL = stub.URL
	defer func() { curseforgeVersionURL = orig }()

	s := settingsTestStore(t)
	r := settingsRouter(s)
	tok := adminToken(t)

	// Set the key.
	body, _ := json.Marshal(map[string]string{"value": "cf-key-value-7890"})
	req := httptest.NewRequest(http.MethodPut, "/settings/integrations/curseforge_api_key", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// List shows it configured with a hint and NO raw value.
	req = httptest.NewRequest(http.MethodGet, "/settings/integrations/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "cf-key-value") {
		t.Fatalf("list leaked the secret value: %s", rr.Body.String())
	}
	var list []struct {
		Key        string `json:"key"`
		Configured bool   `json:"configured"`
		Hint       string `json:"hint"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range list {
		if it.Key == "curseforge_api_key" {
			found = true
			if !it.Configured || it.Hint != "7890" {
				t.Fatalf("unexpected configured/hint: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("curseforge_api_key not in list")
	}

	// Verify the value actually round-trips out of the store.
	got, err := s.GetSecret(context.Background(), "curseforge_api_key")
	if err != nil || got != "cf-key-value-7890" {
		t.Fatalf("GetSecret = %q err=%v", got, err)
	}

	// Delete clears it.
	req = httptest.NewRequest(http.MethodDelete, "/settings/integrations/curseforge_api_key", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d", rr.Code)
	}
	if got, _ := s.GetSecret(context.Background(), "curseforge_api_key"); got != "" {
		t.Fatalf("secret still present after delete: %q", got)
	}
}

func TestSetIntegrationRejectsEmptyValue(t *testing.T) {
	r := settingsRouter(settingsTestStore(t))
	body, _ := json.Marshal(map[string]string{"value": "   "})
	req := httptest.NewRequest(http.MethodPut, "/settings/integrations/curseforge_api_key", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(t))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for empty value", rr.Code)
	}
}
