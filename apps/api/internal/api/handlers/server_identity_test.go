package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

func TestServerIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	icon := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/files/content") && r.URL.Query().Get("path") == "/server.properties":
			_, _ = w.Write([]byte("#Minecraft server properties\nmax-players=40\nmotd=\\u00A7aWelcome to \\u00A7lSMP\nserver-port=25570\n"))
		case strings.HasSuffix(r.URL.Path, "/files/content") && r.URL.Query().Get("path") == "/server-icon.png":
			_, _ = w.Write(icon)
		default:
			http.NotFound(w, r)
		}
	}))
	defer agentSrv.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(agentSrv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, err := f.store.CreateNode(ctx, &store.Node{Name: "fake", FQDN: host, Port: port, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	owner := f.user("owner@example.com", "user")
	srv, err := f.store.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "SMP", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25570, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	h := NewServerHandlers(f.store, t.TempDir())

	get := func() serverIdentity {
		rr := httptest.NewRecorder()
		h.Identity(rr, withURLParams(as(owner, "user", http.MethodGet, "/", nil), "id", srv.ID))
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d", rr.Code)
		}
		var out serverIdentity
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return out
	}
	id := get()
	if id.Address != net.JoinHostPort(host, "25570") || id.AddressSource != "node" {
		t.Fatalf("address %q (%s)", id.Address, id.AddressSource)
	}
	if id.MaxPlayers == nil || *id.MaxPlayers != 40 {
		t.Fatalf("max players: %v", id.MaxPlayers)
	}
	if id.MOTDPlain == nil || *id.MOTDPlain != "Welcome to SMP" {
		t.Fatalf("motd: %v", id.MOTDPlain)
	}
	if id.Icon == nil || !strings.HasPrefix(*id.Icon, "data:image/png;base64,") {
		t.Fatal("icon missing")
	}

	if _, err := validateSettingsChange(nil, []byte(`{"connect_address":"play.example.com"}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSettingsChange(nil, []byte(`{"connect_address":"bad address!"}`), false); err == nil {
		t.Fatal("a malformed address was accepted")
	}
	srv.Settings = []byte(`{"connect_address":"play.example.com"}`)
	if addr, src := connectAddress(srv, node); addr != "play.example.com" || src != "custom" {
		t.Fatalf("custom address: %q %s", addr, src)
	}
}
