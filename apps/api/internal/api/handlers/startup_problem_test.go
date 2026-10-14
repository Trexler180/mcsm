package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/lifecycle"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// fixAgent reports an incompatible-mods startup failure and records what the
// panel asks it to do about it.
type fixAgent struct {
	mu    sync.Mutex
	calls []string
}

func (a *fixAgent) record(c string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, c)
}

func (a *fixAgent) seen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.calls, ",")
}

func (a *fixAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/register"):
	case strings.HasSuffix(r.URL.Path, "/status"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "startup_failure",
			"mod_conflict": map[string]any{
				"detected": true, "kind": "incompatible", "summary": "Async is incompatible with Lithium",
				"suggestions": []map[string]any{{"action": "remove", "mod_id": "async", "mod_name": "Async"}},
				"raw":         []string{"Incompatible mods found!"}, "detected_at": time.Now().Unix(),
			},
		})
	case strings.HasSuffix(r.URL.Path, "/mods/disable"):
		a.record("disable")
		_ = json.NewEncoder(w).Encode(map[string]any{"disabled": []string{"async.jar"}})
	case strings.HasSuffix(r.URL.Path, "/start"):
		a.record("start")
	default:
		http.NotFound(w, r)
	}
}

func TestStartupProblemPlansAndApply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fa := &fixAgent{}
	ts := httptest.NewServer(fa)
	defer ts.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, err := f.store.CreateNode(ctx, &store.Node{Name: "fake", FQDN: host, Port: port, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	owner := f.user("owner@example.com", "user")
	viewer := f.user("viewer@example.com", "user")
	srv, err := f.store.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "Modded", Platform: "fabric",
		MCVersion: "1.21.4", DirectoryPath: "servers/modded", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetServerPermissions(ctx, srv.ID, viewer.ID, []string{"view"}); err != nil {
		t.Fatal(err)
	}

	bus := events.New()
	ops := operations.New(f.store, bus)
	life := lifecycle.New(f.store, ops, bus, nil)
	h := NewModHandlers(f.store, t.TempDir(), nil, nil).WithServices(ops, life)

	rr := httptest.NewRecorder()
	h.StartupProblem(rr, withURLParams(as(owner, "user", http.MethodGet, "/", nil), "id", srv.ID))
	var got startupProblemResponse
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.Problem == nil || got.Problem.Kind != "incompatible" || len(got.Plans) != 1 {
		t.Fatalf("diagnosis: %+v", got)
	}
	plan := got.Plans[0]
	if !strings.HasPrefix(plan.Title, "Disable Async") || len(plan.Steps) != 2 || plan.Steps[1].Kind != "start" {
		t.Fatalf("plan: %+v", plan)
	}

	apply := func(u *store.User, planID string) int {
		rr := httptest.NewRecorder()
		h.ApplyFix(rr, withURLParams(as(u, "user", http.MethodPost, "/", nil), "id", srv.ID, "planId", planID))
		return rr.Code
	}
	if code := apply(viewer, plan.ID); code != http.StatusForbidden {
		t.Fatalf("a view-only member applied a fix: %d", code)
	}
	if code := apply(owner, "not-a-plan"); code != http.StatusConflict {
		t.Fatalf("unknown plan: %d, want 409", code)
	}
	if code := apply(owner, plan.ID); code != http.StatusAccepted {
		t.Fatalf("apply: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for fa.seen() != "disable,start" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fa.seen() != "disable,start" {
		t.Fatalf("agent saw %q, want the disable then the start", fa.seen())
	}
}
