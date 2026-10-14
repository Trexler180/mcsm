package operations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func testEnv(t *testing.T) (*store.Store, *sql.DB, string) {
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
	ctx := context.Background()
	node, _ := s.CreateNode(ctx, &store.Node{Name: "n", FQDN: "localhost", Port: 1, Scheme: "http"}, "t")
	user, _ := s.CreateUser(ctx, "u@example.com", "h", "user")
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: user.ID, Name: "s", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/s", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, srv.ID
}

// Two exclusive operations can never run on one server at once.
func TestExclusiveOperationsHoldTheServer(t *testing.T) {
	s, _, serverID := testEnv(t)
	m := New(s, events.New())
	ctx := context.Background()

	op, err := m.Begin(ctx, serverID, KindAutoUpdate, System, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Begin(ctx, serverID, KindMigrate, System, Options{})
	busy, ok := store.AsServerBusy(err)
	if !ok || busy.Current == nil || busy.Current.Kind != KindAutoUpdate {
		t.Fatalf("second exclusive operation: err = %v, want busy naming the auto update", err)
	}
	if err := m.Guard(ctx, serverID); err == nil {
		t.Fatal("Guard let a short action through while the server was held")
	}
	op.Done(nil, nil)
	if err := m.Guard(ctx, serverID); err != nil {
		t.Fatalf("Guard after the operation ended: %v", err)
	}
	if _, err := m.Begin(ctx, serverID, KindMigrate, System, Options{}); err != nil {
		t.Fatalf("a new operation after the first ended: %v", err)
	}
}

func TestOperationOutcomes(t *testing.T) {
	s, _, serverID := testEnv(t)
	m := New(s, nil)
	ctx := context.Background()

	op, _ := m.Begin(ctx, serverID, KindBackup, System, Options{})
	op.Progress("zipping", "half way", ptr(0.5))
	op.Done(errors.New("disk full"), nil)
	got, _ := s.GetOperation(ctx, op.ID())
	if got.Status != store.OpFailed || got.Error != "disk full" || got.Phase != "zipping" {
		t.Fatalf("failed op = %+v", got)
	}
	op.Done(nil, nil) // a late second finish must not rewrite the outcome
	if got, _ := s.GetOperation(ctx, op.ID()); got.Status != store.OpFailed {
		t.Fatal("a second Done rewrote a finished operation")
	}

	op2, _ := m.Begin(ctx, serverID, KindBackup, System, Options{})
	if !m.Cancel(op2.ID()) {
		t.Fatal("cancel found no running operation")
	}
	<-op2.Context().Done()
	op2.Done(op2.Context().Err(), nil)
	if got, _ := s.GetOperation(ctx, op2.ID()); got.Status != store.OpCancelled {
		t.Fatalf("cancelled op status = %s", got.Status)
	}
}

// Run executes in the background and records the result.
func TestRunRecordsResult(t *testing.T) {
	s, _, serverID := testEnv(t)
	bus := events.New()
	sub, _, _ := bus.Subscribe(0)
	defer sub.Close()
	m := New(s, bus)
	rec, err := m.Run(context.Background(), serverID, KindStart, System, Options{}, func(op *Op) (any, error) {
		return map[string]string{"ok": "yes"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-sub.C():
			if op, ok := e.Data.(*store.Operation); ok && op.ID == rec.ID && op.Status == store.OpSucceeded {
				return
			}
		case <-deadline:
			t.Fatal("no succeeded event for the operation")
		}
	}
}

// After a restart nothing is really running, so records left "running" are
// closed: the operation is interrupted and a stuck backup can be deleted.
func TestSweepClosesWhatARestartCutOff(t *testing.T) {
	s, db, serverID := testEnv(t)
	ctx := context.Background()
	m := New(s, nil)
	if _, err := m.Begin(ctx, serverID, KindBackup, System, Options{}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateBackup(ctx, &store.Backup{ServerID: serverID, Trigger: "manual", Status: "running"})

	swept := New(s, nil).Sweep(ctx)
	if len(swept) != 1 || swept[0].Status != store.OpInterrupted {
		t.Fatalf("swept = %+v", swept)
	}
	var status string
	_ = db.QueryRow(`SELECT status FROM backups WHERE id = ?`, b.ID).Scan(&status)
	if status != "failed" {
		t.Fatalf("a backup left running is %q after the sweep, want failed", status)
	}
	if err := m.Guard(ctx, serverID); err != nil {
		t.Fatalf("the server is still held after the sweep: %v", err)
	}
}

func ptr(f float64) *float64 { return &f }

// A scheduled action's audit row leads back to the task and the run behind it.
func TestScheduledActorNamesItsTask(t *testing.T) {
	s, _, serverID := testEnv(t)
	ctx := context.Background()
	a := Actor{Trigger: TriggerScheduled, TaskID: "task-1", RunID: "run-1"}
	a.Audit(ctx, s, serverID, "server.restart", nil)

	entries, err := s.ListAudit(ctx, serverID, 10)
	if err != nil || len(entries) != 1 || entries[0].Detail == nil {
		t.Fatalf("entries = %v, %v", entries, err)
	}
	for _, want := range []string{`"task_id":"task-1"`, `"task_run_id":"run-1"`, `"trigger":"scheduled"`} {
		if !strings.Contains(*entries[0].Detail, want) {
			t.Errorf("detail %s lacks %s", *entries[0].Detail, want)
		}
	}

	// The actor travels to engines that take no Actor parameter.
	got, ok := ActorFromContext(ContextWithActor(ctx, a))
	if !ok || got.TaskID != "task-1" {
		t.Fatalf("actor from context = %+v, %v", got, ok)
	}
}

// An operation is driven by one goroutine and read by others (a request
// answering with it, the event stream); that must be safe, and Done must
// finish it exactly once.
func TestOperationIsSafeAcrossGoroutines(t *testing.T) {
	s, _, serverID := testEnv(t)
	m := New(s, events.New())
	op, err := m.Begin(context.Background(), serverID, KindBackup, System, Options{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			f := float64(i) / 50
			op.Progress("archiving", "working", &f)
		}
		op.Done(nil, map[string]any{"ok": true})
		op.Done(errors.New("late"), nil) // ignored: the first call won
	}()
	for i := 0; i < 50; i++ {
		_ = op.Record()
	}
	<-done
	rec, err := s.GetOperation(context.Background(), op.ID())
	if err != nil || rec.Status != store.OpSucceeded {
		t.Fatalf("operation ended %+v (%v), want succeeded", rec, err)
	}
	if got := op.Record(); got.Status != store.OpSucceeded {
		t.Fatalf("in-memory record %s after a second Done", got.Status)
	}
}
