package poller

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// fakeAgent answers /info and the batch status endpoint with whatever the test
// sets, or not at all when down.
type fakeAgent struct {
	mu       sync.Mutex
	down     bool
	statuses map[string]map[string]any
}

func (f *fakeAgent) set(id string, st map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = st
}

func (f *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Path {
	case "/agent/v1/info":
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "test", "boot_id": "b1", "memory_mb": 8192,
			"isolation": map[string]any{"mode": "enforced", "active": true, "user": "mcsm-mc"}})
	case "/agent/v1/servers/status":
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": f.statuses})
	default:
		http.NotFound(w, r)
	}
}

type env struct {
	t     *testing.T
	store *store.Store
	db    *sql.DB
	agent *fakeAgent
	p     *Poller
	srv   *store.Server
	node  *store.Node
}

func newEnv(t *testing.T) *env {
	t.Helper()
	name := strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_time_format=datetime&_timezone=UTC")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	_ = goose.SetDialect("sqlite3")
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	s := store.New(db)

	fa := &fakeAgent{statuses: map[string]map[string]any{}}
	ts := httptest.NewServer(fa)
	t.Cleanup(ts.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	ctx := context.Background()
	node, err := s.CreateNode(ctx, &store.Node{Name: "n", FQDN: host, Port: port, Scheme: "http"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.CreateUser(ctx, "o@example.com", "h", "user")
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "smp", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, store: s, db: db, agent: fa, p: New(s, nil, events.New()), srv: srv, node: node}
}

func (e *env) status() string {
	srv, err := e.store.GetServer(context.Background(), e.srv.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return srv.Status
}

func (e *env) auditCount(action string) int {
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND server_id = ?`, action, e.srv.ID).Scan(&n)
	return n
}

func (e *env) lastEndReason() string {
	var reason sql.NullString
	_ = e.db.QueryRow(`SELECT end_reason FROM server_uptime WHERE server_id = ? ORDER BY id DESC LIMIT 1`, e.srv.ID).Scan(&reason)
	return reason.String
}

func (e *env) sweep() { e.p.Sweep(context.Background()) }

// A real crash is reported by the agent as "crashed". It must be recorded as a
// crash — the old poller only treated online->offline as one, so real crashes
// were silent.
func TestCrashReportedByAgentIsACrash(t *testing.T) {
	e := newEnv(t)
	e.agent.set(e.srv.ID, map[string]any{"status": "online", "run_id": "r1", "started_at": time.Now()})
	e.sweep()
	if e.status() != "online" {
		t.Fatalf("status = %s, want online", e.status())
	}
	code := 1
	e.agent.set(e.srv.ID, map[string]any{"status": "crashed", "run_id": "r1", "exit_cause": "crashed", "exit_code": code})
	e.sweep()
	if e.status() != "crashed" {
		t.Fatalf("status = %s, want crashed", e.status())
	}
	if e.auditCount("server.crash") != 1 {
		t.Fatal("a crash reported by the agent was not recorded as a crash")
	}
	if e.lastEndReason() != "crash" {
		t.Fatalf("uptime segment ended as %q, want crash", e.lastEndReason())
	}
}

// /stop typed in the game is a clean exit, not a crash.
func TestCleanExitIsNotACrash(t *testing.T) {
	e := newEnv(t)
	e.agent.set(e.srv.ID, map[string]any{"status": "online", "run_id": "r1"})
	e.sweep()
	e.agent.set(e.srv.ID, map[string]any{"status": "offline", "run_id": "r1", "exit_cause": "exited", "exit_code": 0})
	e.sweep()
	if e.status() != "offline" || e.auditCount("server.crash") != 0 {
		t.Fatalf("status = %s, crashes = %d; a clean exit is a stop", e.status(), e.auditCount("server.crash"))
	}
	if e.lastEndReason() != "stop" {
		t.Fatalf("uptime ended as %q, want stop", e.lastEndReason())
	}
}

// An agent that is briefly unreachable (a deploy restarting it) changes
// nothing. One unreachable for longer makes the server "unknown" — never a
// crash — and when the agent returns with the same process still running, the
// uptime segment is reopened as if the outage never happened.
func TestUnreachableNodeIsUnknownNotCrashed(t *testing.T) {
	e := newEnv(t)
	started := time.Now().Add(-time.Hour)
	e.agent.set(e.srv.ID, map[string]any{"status": "online", "run_id": "r1", "started_at": started})
	e.sweep()

	e.agent.mu.Lock()
	e.agent.down = true
	e.agent.mu.Unlock()
	e.sweep() // within the freshness window: a restart, nothing changes
	if e.status() != "online" {
		t.Fatalf("a momentary outage changed the status to %s", e.status())
	}

	// Age the heartbeat past the window.
	if _, err := e.db.Exec(`UPDATE nodes SET last_seen = ? WHERE id = ?`, time.Now().Add(-2*time.Minute).UTC().Format("2006-01-02 15:04:05"), e.node.ID); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	if e.status() != "unknown" {
		t.Fatalf("status = %s, want unknown while the node is unreachable", e.status())
	}
	if e.auditCount("server.crash") != 0 {
		t.Fatal("an unreachable node was recorded as a crash")
	}

	e.agent.mu.Lock()
	e.agent.down = false
	e.agent.mu.Unlock()
	e.sweep()
	if e.status() != "online" {
		t.Fatalf("status = %s after the node came back", e.status())
	}
	var open int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM server_uptime WHERE server_id = ? AND ended_at IS NULL`, e.srv.ID).Scan(&open)
	var segments int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM server_uptime WHERE server_id = ?`, e.srv.ID).Scan(&segments)
	if open != 1 || segments != 1 {
		t.Fatalf("uptime: %d open of %d segments; the same process should keep one unbroken segment", open, segments)
	}
}

// A server with no run on the agent while its first start is still installing
// keeps "starting".
func TestInstallingStartIsNotOverwritten(t *testing.T) {
	e := newEnv(t)
	if err := e.store.UpdateServerStatus(context.Background(), e.srv.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	if e.status() != "starting" {
		t.Fatalf("status = %s, want starting while installing", e.status())
	}
}

// The node keeps what its agent reports about server isolation.
func TestNodeRecordsIsolation(t *testing.T) {
	e := newEnv(t)
	e.sweep()
	node, err := e.store.GetNode(context.Background(), e.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(node.Isolation), `"active":true`) {
		t.Fatalf("isolation = %s", node.Isolation)
	}
}
