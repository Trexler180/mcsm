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
	"time"

	"github.com/mcsm/api/internal/store"
)

// Loads of the Mods tab share one background rescan per server, and a rescan
// is not repeated while the last one is fresh.
func TestModRescanIsSingleFlightAndCached(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Modded")
	h := NewModHandlers(f.store, t.TempDir(), nil, nil)

	first, state := h.startRescan(srv.ID, false)
	if state != rescanStarted {
		t.Fatalf("first load: %s, want started", state)
	}
	if _, state := h.startRescan(srv.ID, false); state != rescanRunning && state != rescanFresh {
		t.Fatalf("second load during a rescan: %s", state)
	}
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the rescan never finished")
	}
	if _, state := h.startRescan(srv.ID, false); state != rescanFresh {
		t.Fatalf("load right after a rescan: %s, want fresh", state)
	}
	// An explicit rescan ignores freshness.
	forced, state := h.startRescan(srv.ID, true)
	if state != rescanStarted {
		t.Fatalf("forced rescan: %s, want started", state)
	}
	<-forced
}

// A rescan waits for a mod change in progress on the same server, so it never
// lists the folder between an install writing its jar and recording it (which
// adopted the jar as a second row for the same file).
func TestModRescanWaitsForAModChange(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Modded")
	other := f.server(owner, "Other")
	h := NewModHandlers(f.store, t.TempDir(), nil, nil)

	unlock := h.lockMods(srv.ID)
	done, _ := h.startRescan(srv.ID, true)
	select {
	case <-done:
		t.Fatal("the rescan ran while an install held the server's mods")
	case <-time.After(200 * time.Millisecond):
	}
	// Another server's rescan is not held up.
	otherDone, _ := h.startRescan(other.ID, true)
	select {
	case <-otherDone:
	case <-time.After(10 * time.Second):
		t.Fatal("another server's rescan waited on this one's lock")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the rescan never ran after the install finished")
	}
}

// Two rows for one jar (a rescan adopted it while an install was recording it)
// are folded into one on the next rescan, keeping the known project.
func TestModReconcileFoldsDuplicateRowsForOneJar(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case strings.HasSuffix(r.URL.Path, "/files") && r.URL.Query().Get("path") == "/plugins":
			_ = json.NewEncoder(w).Encode(map[string]any{"path": "/plugins", "entries": []map[string]any{{"name": "Chunky-1.5.3.jar", "type": "file", "size": 1}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, err := f.store.CreateNode(ctx, &store.Node{Name: "fake", FQDN: host, Port: port, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	owner := f.user("owner@example.com", "user")
	srv, err := f.store.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "Plugins", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/plugins", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	sha := "abc"
	if _, err := f.store.CreateMod(ctx, &store.InstalledMod{ServerID: srv.ID, Source: "custom", Name: "Chunky", Version: "unknown",
		FileName: "Chunky-1.5.3.jar", SHA512: &sha, InstallPath: "/plugins"}); err != nil {
		t.Fatal(err)
	}
	pid, vid := "4", "24967"
	if _, err := f.store.CreateMod(ctx, &store.InstalledMod{ServerID: srv.ID, Source: "hangar", SourceID: &pid, VersionID: &vid, Name: "Chunky",
		Version: "1.5.3", FileName: "Chunky-1.5.3.jar", InstallPath: "/plugins"}); err != nil {
		t.Fatal(err)
	}

	h := NewModHandlers(f.store, t.TempDir(), nil, nil)
	h.reconcileFromDisk(ctx, srv.ID)

	mods, err := f.store.ListMods(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Source != "hangar" {
		got := []string{}
		for _, m := range mods {
			got = append(got, m.Source+":"+m.FileName)
		}
		t.Fatalf("rows after the rescan: %v, want the one hangar row", got)
	}
}
