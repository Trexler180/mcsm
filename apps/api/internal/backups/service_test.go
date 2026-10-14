package backups

import (
	"context"
	"database/sql"
	"errors"
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

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

type fakeAgent struct {
	mu        sync.Mutex
	calls     []string
	backupErr error
}

func (a *fakeAgent) record(c string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, c)
}

func (a *fakeAgent) seen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.calls, ",")
}

func (a *fakeAgent) RegisterDir(context.Context, string, string) error { return nil }
func (a *fakeAgent) Backup(_ context.Context, _, backupID string) (*agent.BackupResult, error) {
	a.record("backup")
	if a.backupErr != nil {
		return nil, a.backupErr
	}
	return &agent.BackupResult{BackupID: backupID, SizeBytes: 1024}, nil
}
func (a *fakeAgent) Restore(_ context.Context, _, backupID string) error {
	a.record("restore:" + backupID)
	return nil
}
func (a *fakeAgent) DeleteBackup(context.Context, string, string) error {
	a.record("delete")
	return nil
}

type env struct {
	db  *sql.DB
	s   *store.Store
	srv *store.Server
	ops *operations.Manager
}

func newEnv(t *testing.T, node *store.Node) *env {
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
	if node == nil {
		node = &store.Node{Name: "n", FQDN: "localhost", Port: 1, Scheme: "http"}
	}
	n, err := s.CreateNode(ctx, node, "t")
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.CreateUser(ctx, "owner@example.com", "h", "user")
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: n.ID, OwnerID: owner.ID, Name: "SMP", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, s: s, srv: srv, ops: operations.New(s, events.New())}
}

func (e *env) successfulBackup(t *testing.T, startedAt time.Time) *store.Backup {
	t.Helper()
	ctx := context.Background()
	b, err := e.s.CreateBackup(ctx, &store.Backup{ServerID: e.srv.ID, Trigger: "manual", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	size := int64(10)
	if err := e.s.UpdateBackupResult(ctx, b.ID, "success", &size, ""); err != nil {
		t.Fatal(err)
	}
	if !startedAt.IsZero() {
		if _, err := e.db.Exec(`UPDATE backups SET started_at = ? WHERE id = ?`, startedAt, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func (e *env) waitDone(t *testing.T, opID string) *store.Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op, err := e.s.GetOperation(context.Background(), opID)
		if err == nil && op.Status != store.OpRunning {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the operation never finished")
	return nil
}

// Restoring backs up the current world first, so restoring the wrong backup
// loses nothing.
func TestRestoreTakesASafetyBackupFirst(t *testing.T) {
	e := newEnv(t, nil)
	a := &fakeAgent{}
	svc := NewService(e.s, e.ops, nil, nil, func(context.Context, *store.Server) (Agent, error) { return a, nil })
	target := e.successfulBackup(t, time.Time{})

	op, err := svc.Restore(context.Background(), e.srv, target.ID, operations.System, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := e.waitDone(t, op.ID); done.Status != store.OpSucceeded {
		t.Fatalf("restore ended %s: %s", done.Status, done.Error)
	}
	if got := a.seen(); got != "backup,restore:"+target.ID {
		t.Fatalf("node calls %q, want the safety backup before the restore", got)
	}
	list, _ := e.s.ListBackups(context.Background(), e.srv.ID)
	var safety bool
	for _, b := range list {
		if b.Trigger == "pre_restore" && b.Status == "success" {
			safety = true
		}
	}
	if !safety {
		t.Fatal("no pre_restore backup was recorded")
	}
}

// A safety backup that fails stops the restore before it touches anything.
func TestRestoreStopsWhenTheSafetyBackupFails(t *testing.T) {
	e := newEnv(t, nil)
	a := &fakeAgent{backupErr: errors.New("disk full")}
	svc := NewService(e.s, e.ops, nil, nil, func(context.Context, *store.Server) (Agent, error) { return a, nil })
	target := e.successfulBackup(t, time.Time{})

	op, err := svc.Restore(context.Background(), e.srv, target.ID, operations.System, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := e.waitDone(t, op.ID); done.Status != store.OpFailed || !strings.Contains(done.Error, "nothing was restored") {
		t.Fatalf("restore ended %s: %s", done.Status, done.Error)
	}
	if strings.Contains(a.seen(), "restore") {
		t.Fatal("the world was replaced although its safety backup failed")
	}
}

// Age-based retention never deletes the newest successful backup, even when
// every backup is past the window.
func TestRetentionKeepsTheNewestSuccessfulBackup(t *testing.T) {
	var deleted []string
	var mu sync.Mutex
	nodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deleted = append(deleted, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer nodeSrv.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(nodeSrv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	e := newEnv(t, &store.Node{Name: "n", FQDN: host, Port: port, Scheme: "http"})
	ctx := context.Background()
	if _, err := e.s.CreateBackupTarget(ctx, &store.BackupTarget{ServerID: e.srv.ID, Name: "local", Type: "local",
		Retention: []byte(`{"max_age_days":7}`), IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldest := e.successfulBackup(t, now.AddDate(0, 0, -40))
	older := e.successfulBackup(t, now.AddDate(0, 0, -30))
	newest := e.successfulBackup(t, now.AddDate(0, 0, -20))

	Enforce(ctx, e.s, e.srv.ID)

	mu.Lock()
	got := strings.Join(deleted, ",")
	mu.Unlock()
	if strings.Contains(got, newest.ID) {
		t.Fatal("retention deleted the only backup left to restore")
	}
	if !strings.Contains(got, oldest.ID) || !strings.Contains(got, older.ID) {
		t.Fatalf("expired backups kept: deleted %q", got)
	}
	left, _ := e.s.ListBackups(ctx, e.srv.ID)
	if len(left) != 1 || left[0].ID != newest.ID {
		t.Fatalf("%d backups left", len(left))
	}
}
