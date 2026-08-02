package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func publicStatusTestStore(t *testing.T) *store.Store {
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

// publicStatusFixture creates a server; public exposure is left to each test.
func publicStatusFixture(t *testing.T, s *store.Store) *store.Server {
	t.Helper()
	ctx := context.Background()
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID: node.ID, OwnerID: user.ID, Name: "Survival World", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/survival", JavaBinary: "java",
		Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func exposePublic(t *testing.T, s *store.Store, srv *store.Server, slug string) {
	t.Helper()
	srv.PublicStatus = true
	srv.PublicSlug = slug
	if err := s.UpdateServer(context.Background(), srv.ID, srv); err != nil {
		t.Fatal(err)
	}
}

func publicStatusRouter(s *store.Store) http.Handler {
	h := NewPublicStatusHandlers(s)
	r := chi.NewRouter()
	r.Get("/status/{slug}", h.Page)
	r.Get("/status/{slug}/og.png", h.OGImage)
	r.Get("/api/v1/public/status/{slug}", h.JSON)
	return r
}

func TestPublicStatusHiddenUnlessEnabled(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	router := publicStatusRouter(s)

	// Slug set but page disabled — must still be a 404.
	srv.PublicSlug = "survival"
	srv.PublicStatus = false
	if err := s.UpdateServer(context.Background(), srv.ID, srv); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/status/survival",
		"/api/v1/public/status/survival",
		"/status/nope",
		"/status/../../etc/passwd",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestPublicStatusExposesOnlyMinimalFields(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	exposePublic(t, s, srv, "survival")

	// Bring it online so uptime fields populate.
	if err := s.UpdateServerStatus(context.Background(), srv.ID, "online"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/status/survival", nil)
	rec := httptest.NewRecorder()
	publicStatusRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["name"] != "Survival World" || payload["online"] != true {
		t.Fatalf("unexpected payload: %v", payload)
	}
	if payload["online_since"] == nil {
		t.Fatal("online_since missing for online server")
	}
	if _, ok := payload["days"]; !ok {
		t.Fatal("days strip missing")
	}
	// Nothing internal may leak.
	for _, key := range []string{
		"id", "node_id", "owner_id", "port", "directory_path", "java_binary",
		"jvm_args", "settings", "tags", "ram_mb_min", "ram_mb_max", "status",
	} {
		if _, ok := payload[key]; ok {
			t.Fatalf("public payload leaks %q", key)
		}
	}
	raw := rec.Body.String()
	for _, needle := range []string{srv.ID, "25565", "servers/survival"} {
		if strings.Contains(raw, needle) {
			t.Fatalf("public payload contains %q", needle)
		}
	}
}

func TestPublicStatusPageRendersHTML(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	exposePublic(t, s, srv, "survival")

	req := httptest.NewRequest(http.MethodGet, "/status/survival", nil)
	rec := httptest.NewRecorder()
	publicStatusRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Survival World") || !strings.Contains(body, "Offline") {
		t.Fatalf("page missing expected content:\n%s", body[:min(400, len(body))])
	}
	if strings.Contains(body, "25565") || strings.Contains(body, srv.ID) {
		t.Fatal("page leaks internal data")
	}
}

func TestPublicStatusHostRouting(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	exposePublic(t, s, srv, "survival")

	h := NewPublicStatusHandlers(s)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // marks "passed through"
	})
	wrapped := h.HostRouter("status.example.com")(inner)

	cases := []struct {
		host, path string
		want       int
	}{
		{"survival.status.example.com", "/", http.StatusOK},
		{"survival.status.example.com:443", "/", http.StatusOK},
		{"survival.status.example.com", "/status.json", http.StatusOK},
		{"survival.status.example.com", "/og.png", http.StatusOK},
		{"survival.status.example.com", "/favicon.ico", http.StatusNoContent},
		{"survival.status.example.com", "/api/v1/servers", http.StatusNotFound},
		{"unknown.status.example.com", "/", http.StatusNotFound},
		{"deep.survival.status.example.com", "/", http.StatusTeapot}, // nested label: pass through
		{"status.example.com", "/", http.StatusTeapot},               // apex: pass through
		{"panel.example.com", "/", http.StatusTeapot},                // unrelated host
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Fatalf("host %s path %s: status %d, want %d", c.host, c.path, rec.Code, c.want)
		}
	}
}

func TestValidatePublicSlug(t *testing.T) {
	valid := []string{"survival", "my-server", "abc", "a1b2c3", "sv-01", "test", "dev"}
	for _, s := range valid {
		if err := ValidatePublicSlug(s); err != nil {
			t.Fatalf("%q rejected: %v", s, err)
		}
	}
	invalid := []string{
		"", "ab", "-abc", "abc-", "UPPER", "has space", "dot.dot", "under_score",
		"www", "api", "status", "admin", strings.Repeat("a", 64), "héllo",
	}
	for _, s := range invalid {
		if err := ValidatePublicSlug(s); err == nil {
			t.Fatalf("%q accepted, want error", s)
		}
	}
}

func TestPublicStatusEmbedMeta(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	exposePublic(t, s, srv, "survival")

	req := httptest.NewRequest(http.MethodGet, "/status/survival", nil)
	req.Host = "panel.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	publicStatusRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, needle := range []string{
		`<meta property="og:title" content="Survival World is Offline">`,
		`<meta property="og:image" content="https://panel.example.com/status/survival/og.png">`,
		`<meta property="og:url" content="https://panel.example.com/status/survival">`,
		`<meta name="theme-color" content="#ef4444">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`property="og:description"`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("page missing %s", needle)
		}
	}
}

func TestPublicStatusOGImage(t *testing.T) {
	s := publicStatusTestStore(t)
	srv := publicStatusFixture(t, s)
	exposePublic(t, s, srv, "survival")

	router := publicStatusRouter(s)
	req := httptest.NewRequest(http.MethodGet, "/status/survival/og.png", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type %q", ct)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Fatalf("image is %dx%d, want 1200x630", b.Dx(), b.Dy())
	}

	req = httptest.NewRequest(http.MethodGet, "/status/nope/og.png", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug og.png: status %d, want 404", rec.Code)
	}
}

func TestEmbedDescription(t *testing.T) {
	players := 12
	avail := 99.987
	online := &publicStatusView{
		Online: true, OnlineSince: 1000, MCVersion: "1.21.4",
		Players: &players, Avail90d: &avail,
	}
	got := embedDescription(online, 1000+3*86400+4*3600)
	want := "🟢 Online for 3d 4h · 12 players · Minecraft 1.21.4 · 99.987% uptime (90d)"
	if got != want {
		t.Fatalf("online: %q, want %q", got, want)
	}

	offline := &publicStatusView{Online: false, OfflineSince: 1000}
	got = embedDescription(offline, 1000+150)
	if got != "🔴 Offline for 2m" {
		t.Fatalf("offline: %q", got)
	}
}

func TestFmtPct(t *testing.T) {
	cases := map[float64]string{
		100:      "100%",
		99.9:     "99.9%",
		99.99:    "99.99%",
		99.999:   "99.999%",
		99.9994:  "99.999%",
		99.98765: "99.988%",
		0:        "0%",
	}
	for in, want := range cases {
		if got := fmtPct(in); got != want {
			t.Fatalf("fmtPct(%v) = %q, want %q", in, got, want)
		}
	}
}
