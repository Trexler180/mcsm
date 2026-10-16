package scheduler

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

	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func TestValidateCron(t *testing.T) {
	for expr, ok := range map[string]bool{
		"0 4 * * *": true, "*/5 * * * *": true, "@daily": true, "@every 1h": true,
		"not a schedule": false, "@every 10s": false, "* * * * * *": false,
	} {
		if err := ValidateCron(expr); (err == nil) != ok {
			t.Errorf("%q: err = %v", expr, err)
		}
	}
}

// commandNode records console commands sent to it.
type commandNode struct {
	mu       sync.Mutex
	commands []string
	// stopped answers commands the way an agent does for a server that isn't running.
	stopped bool
}

func (n *commandNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/command") && n.stopped {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"server not running"}`))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/command") {
		var body struct {
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n.mu.Lock()
		n.commands = append(n.commands, body.Command)
		n.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (n *commandNode) sent() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.commands, "|")
}

type env struct {
	s     *store.Store
	sched *Scheduler
	node  *commandNode
	srv   *store.Server
	owner *store.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_time_format=datetime&_timezone=UTC")
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
	ctx := context.Background()

	cn := &commandNode{}
	ts := httptest.NewServer(cn)
	t.Cleanup(ts.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, err := s.CreateNode(ctx, &store.Node{Name: "n", FQDN: host, Port: port, Scheme: "http"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.CreateUser(ctx, "owner@example.com", "h", "user")
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "SMP", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return &env{s: s, sched: New(s, nil, nil, nil, nil, nil), node: cn, srv: srv, owner: owner}
}

func (e *env) task(t *testing.T, createdBy *store.User, command string) *store.ScheduledTask {
	t.Helper()
	var by *string
	if createdBy != nil {
		by = &createdBy.ID
	}
	task, err := e.s.CreateTask(context.Background(), &store.ScheduledTask{
		ServerID: e.srv.ID, Name: "Announce", CronExpr: "0 * * * *", Action: "command",
		Payload: json.RawMessage(`{"command":"` + command + `"}`), Enabled: true, CreatedBy: by,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *env) lastRun(t *testing.T, taskID string) *store.TaskRun {
	t.Helper()
	runs, err := e.s.ListTaskRuns(context.Background(), e.srv.ID, taskID, 10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("no run recorded: %v", err)
	}
	return runs[0]
}

// An edited task runs as it is now: its new command, not the one it was
// registered with — and the edit re-registers it.
func TestEditedTaskTakesEffect(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task := e.task(t, e.owner, "say before")
	e.sched.refresh(ctx)
	before := e.sched.entries[task.ID]

	e.sched.fire(task.ID)
	task.Payload = json.RawMessage(`{"command":"say after"}`)
	if err := e.s.UpdateTask(ctx, task.ID, task); err != nil {
		t.Fatal(err)
	}
	e.sched.refresh(ctx)
	if e.sched.entries[task.ID].fingerprint == before.fingerprint {
		t.Fatal("editing the command did not re-register the task")
	}
	e.sched.fire(task.ID)

	if got := e.node.sent(); got != "say before|say after" {
		t.Fatalf("commands sent %q", got)
	}
	if run := e.lastRun(t, task.ID); run.Status != "succeeded" {
		t.Fatalf("run %+v", run)
	}
}

// A task never outlives its creator's permission: it is skipped, recorded as
// skipped, and nothing reaches the server.
func TestTaskOfARevokedCreatorIsSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	helper, _ := e.s.CreateUser(ctx, "helper@example.com", "h", "user")
	if err := e.s.SetServerPermissions(ctx, e.srv.ID, helper.ID, []string{"console"}); err != nil {
		t.Fatal(err)
	}
	task := e.task(t, helper, "say hello")
	if err := e.s.SetServerPermissions(ctx, e.srv.ID, helper.ID, []string{"view"}); err != nil {
		t.Fatal(err)
	}

	e.sched.fire(task.ID)
	if got := e.node.sent(); got != "" {
		t.Fatalf("a revoked creator's command reached the server: %q", got)
	}
	if run := e.lastRun(t, task.ID); run.Status != "skipped" {
		t.Fatalf("run %+v, want skipped", run)
	}
}

// A disabled task is dropped from the schedule.
func TestDisabledTaskIsUnregistered(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task := e.task(t, e.owner, "say hi")
	e.sched.refresh(ctx)
	if _, ok := e.sched.entries[task.ID]; !ok {
		t.Fatal("an enabled task was not registered")
	}
	task.Enabled = false
	if err := e.s.UpdateTask(ctx, task.ID, task); err != nil {
		t.Fatal(err)
	}
	e.sched.refresh(ctx)
	if _, ok := e.sched.entries[task.ID]; ok {
		t.Fatal("a disabled task stayed scheduled")
	}
}

// Run now runs the task straight away as the person asking, recorded in its
// history like any other run, whether or not it's paused.
func TestRunNowRunsAsTheRequester(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task := e.task(t, nil, "say now")
	task.Enabled = false
	if err := e.s.UpdateTask(ctx, task.ID, task); err != nil {
		t.Fatal(err)
	}
	run, err := e.sched.RunNow(task, e.owner.ID)
	if err != nil || run.Status != "running" {
		t.Fatalf("run %+v, err %v", run, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.lastRun(t, task.ID).Status == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := e.lastRun(t, task.ID); got.Status != "succeeded" || got.ID != run.ID {
		t.Fatalf("run %+v", got)
	}
	if got := e.node.sent(); got != "say now" {
		t.Fatalf("commands sent %q", got)
	}
}

// A console command for a server that's stopped is a message nobody would
// hear: the run is recorded as skipped, with the reason, rather than failed.
func TestCommandForAStoppedServerIsSkipped(t *testing.T) {
	e := newEnv(t)
	e.node.stopped = true
	task := e.task(t, e.owner, "say restarting soon")

	e.sched.fire(task.ID)
	run := e.lastRun(t, task.ID)
	if run.Status != "skipped" || run.Message != "the server wasn't running" {
		t.Fatalf("run %+v, want skipped because the server wasn't running", run)
	}
}

// A scheduled restart leaves a stopped server stopped: someone may have
// stopped it on purpose, and restarting would start it.
func TestRestartOfAStoppedServerIsSkipped(t *testing.T) {
	e := newEnv(t)
	task, err := e.s.CreateTask(context.Background(), &store.ScheduledTask{
		ServerID: e.srv.ID, Name: "Nightly restart", CronExpr: "0 5 * * *", Action: "restart", Enabled: true, CreatedBy: &e.owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sched.fire(task.ID)
	if run := e.lastRun(t, task.ID); run.Status != "skipped" || run.Message != "the server wasn't running" {
		t.Fatalf("run %+v, want skipped because the server wasn't running", run)
	}
}
