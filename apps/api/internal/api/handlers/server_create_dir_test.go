package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// A new server never lands in a folder that already holds one (a server
// deleted with its files kept, or another server's): it would take over that
// world and those plugins without anyone choosing to.
func TestNewServerGetsAFolderOfItsOwn(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	onDisk := []string{"survival", "Survival-2", "creative"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import/scan") {
			out := []map[string]string{}
			for _, d := range onDisk {
				out = append(out, map[string]string{"directory": d})
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer ts.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, err := f.store.CreateNode(ctx, &store.Node{Name: "fake", FQDN: host, Port: port, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	admin := f.user("admin@example.com", "admin")
	h := NewServerHandlers(f.store, t.TempDir())

	create := func(name, extra string) map[string]any {
		t.Helper()
		body := `{"name":"` + name + `","node_id":"` + node.ID + `","platform":"paper","mc_version":"1.21.4","port":25565,"ram_mb_max":1024` + extra + `}`
		rr := httptest.NewRecorder()
		h.Create(rr, as(admin, "admin", http.MethodPost, "/", strings.NewReader(body)))
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rr.Code, rr.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return out
	}
	dirOf := func(v map[string]any) string { return filepath.Base(v["directory_path"].(string)) }

	if got := dirOf(create("Survival", "")); got != "survival-3" {
		t.Fatalf("Survival went in %q, want survival-3 (survival and survival-2 are taken)", got)
	}
	if got := dirOf(create("Skyblock", "")); got != "skyblock" {
		t.Fatalf("Skyblock went in %q, want its own name when it is free", got)
	}
}
