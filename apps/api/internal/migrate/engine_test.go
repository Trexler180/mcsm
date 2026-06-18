package migrate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// ── Fakes ────────────────────────────────────────────────────────────

// fakeAgent simulates a node agent for migration: it tracks which jars are "on
// disk", supports a backup/restore snapshot, and decides each boot from whether
// a jar marked broken is present.
type fakeAgent struct {
	mu          sync.Mutex
	files       map[string]bool
	brokenFiles map[string]bool
	snapshots   map[string]map[string]bool // backupID -> files snapshot
	status      string
	startCalls  int
	backupErr   error
}

func newFakeAgent(files ...string) *fakeAgent {
	a := &fakeAgent{files: map[string]bool{}, brokenFiles: map[string]bool{}, snapshots: map[string]map[string]bool{}, status: "offline"}
	for _, f := range files {
		a.files[f] = true
	}
	return a
}

func (a *fakeAgent) RegisterDir(ctx context.Context, serverID, directory string) error { return nil }

func (a *fakeAgent) StartServer(ctx context.Context, serverID string, cfg map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.startCalls++
	a.status = "online"
	for f := range a.files {
		if a.brokenFiles[f] {
			a.status = "crashed"
			break
		}
	}
	return nil
}

func (a *fakeAgent) StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = "offline"
	return nil
}

func (a *fakeAgent) GetStatus(ctx context.Context, serverID string) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"status": a.status}, nil
}

func (a *fakeAgent) Reinstall(ctx context.Context, serverID string, cfg map[string]any) error {
	return nil
}

func (a *fakeAgent) UploadFile(ctx context.Context, serverID, destDir, filename, localPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.files[filename] = true
	return nil
}

func (a *fakeAgent) DeleteFile(ctx context.Context, serverID, p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.files, path.Base(p))
	return nil
}

func (a *fakeAgent) RenameFile(ctx context.Context, serverID, from, to string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	fb, tb := path.Base(from), path.Base(to)
	if a.files[fb] {
		delete(a.files, fb)
		a.files[tb] = true
	}
	return nil
}

func (a *fakeAgent) Backup(ctx context.Context, serverID, backupID string) (*agent.BackupResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.backupErr != nil {
		return nil, a.backupErr
	}
	snap := map[string]bool{}
	for f := range a.files {
		snap[f] = true
	}
	a.snapshots[backupID] = snap
	return &agent.BackupResult{BackupID: backupID, SizeBytes: 1}, nil
}

func (a *fakeAgent) Restore(ctx context.Context, serverID, backupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap, ok := a.snapshots[backupID]
	if !ok {
		return errors.New("no such backup")
	}
	a.files = map[string]bool{}
	for f := range snap {
		a.files[f] = true
	}
	return nil
}

func (a *fakeAgent) hasFile(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.files[name]
}

// fakeSource serves canned version listings keyed by target MC version, and a
// generic downloader.
type fakeSource struct {
	// byTarget[projectID][mcVersion] -> versions for that target.
	byTarget map[string]map[string][]modrinth.Version
}

func (f *fakeSource) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	if m, ok := f.byTarget[projectID]; ok {
		return m[mcVersion], nil
	}
	return nil, nil
}

func (f *fakeSource) Download(ctx context.Context, fileURL, wantSHA string) (string, error) {
	tmp, err := os.CreateTemp("", "fake-jar-*.jar")
	if err != nil {
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

func mkVersion(id, projectID, number, filename string) modrinth.Version {
	v := modrinth.Version{ID: id, ProjectID: projectID, Name: "v" + number, VersionNumber: number}
	file := modrinth.VersionFile{URL: "https://cdn.example/" + filename, Filename: filename, Primary: true}
	file.Hashes.SHA256 = "sha-" + id
	v.Files = []modrinth.VersionFile{file}
	return v
}

// ── Harness ──────────────────────────────────────────────────────────

func testStore(t *testing.T) *store.Store {
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

type fixture struct {
	store  *store.Store
	engine *Engine
	agent  *fakeAgent
	server *store.Server
}

func newFixture(t *testing.T, fa *fakeAgent, source *fakeSource) *fixture {
	t.Helper()
	ctx := context.Background()
	s := testStore(t)

	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{
		NodeID:        node.ID,
		OwnerID:       user.ID,
		Name:          "smp",
		Platform:      "fabric",
		MCVersion:     "1.21.4",
		DirectoryPath: "servers/smp",
		JavaBinary:    "java",
		Port:          25565,
		RAMMbMin:      512,
		RAMMbMax:      2048,
	})
	if err != nil {
		t.Fatal(err)
	}

	e := &Engine{
		store:         s,
		resolveSource: func(string) modSource { return source },
		download:      source,
		newAgent:      func(n *store.Node) agentAPI { return fa },
		active:        map[string]bool{},
		pollInterval:  time.Millisecond,
		offlineGrace:  50 * time.Millisecond,
		stableFor:     0,
		startTimeout:  2 * time.Second,
		runTimeout:    30 * time.Second,
	}
	return &fixture{store: s, engine: e, agent: fa, server: srv}
}

func (f *fixture) addMod(t *testing.T, projectID, versionID, version, filename string) *store.InstalledMod {
	t.Helper()
	pid, vid := projectID, versionID
	m, err := f.store.CreateMod(context.Background(), &store.InstalledMod{
		ServerID:    f.server.ID,
		Source:      "modrinth",
		SourceID:    &pid,
		VersionID:   &vid,
		Name:        projectID,
		Version:     version,
		FileName:    filename,
		Enabled:     true,
		InstallPath: "/mods",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) migrateAndWait(t *testing.T, target string) *store.VersionMigration {
	t.Helper()
	ctx := context.Background()
	run, err := f.engine.Trigger(ctx, f.server.ID, target)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := f.store.GetVersionMigration(ctx, run.ID)
		if err != nil {
			t.Fatalf("GetVersionMigration: %v", err)
		}
		if got.Status != "running" {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("migration did not finish in time")
	return nil
}

// ── Flows ────────────────────────────────────────────────────────────

// Healthy migration: a compatible mod is moved to the target build, an
// incompatible mod is disabled, the server boots, and the change sticks.
func TestMigrateHealthy(t *testing.T) {
	fa := newFakeAgent("a-1.0.jar", "b-1.0.jar")
	source := &fakeSource{byTarget: map[string]map[string][]modrinth.Version{
		"projA": {"1.21.5": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar")}},
		"projB": {"1.21.5": {}}, // no build for target → disable
	}}
	f := newFixture(t, fa, source)
	modA := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")
	modB := f.addMod(t, "projB", "vB1", "1.0", "b-1.0.jar")

	run := f.migrateAndWait(t, "1.21.5")
	if run.Status != "success" {
		t.Fatalf("status = %q (%s), want success", run.Status, run.Detail)
	}

	ctx := context.Background()
	srv, _ := f.store.GetServer(ctx, f.server.ID)
	if srv.MCVersion != "1.21.5" {
		t.Fatalf("server version = %q, want 1.21.5", srv.MCVersion)
	}
	gotA, _ := f.store.GetMod(ctx, modA.ID)
	if gotA.VersionID == nil || *gotA.VersionID != "vA2" || gotA.FileName != "a-2.0.jar" || !gotA.Enabled {
		t.Fatalf("modA not migrated: %+v", gotA)
	}
	gotB, _ := f.store.GetMod(ctx, modB.ID)
	if gotB.Enabled || gotB.FileName != "b-1.0.jar.disabled" {
		t.Fatalf("modB should be disabled: %+v", gotB)
	}
	if !fa.hasFile("a-2.0.jar") || fa.hasFile("a-1.0.jar") {
		t.Fatalf("jar A not swapped: %v", fa.files)
	}
	if !fa.hasFile("b-1.0.jar.disabled") || fa.hasFile("b-1.0.jar") {
		t.Fatalf("jar B not disabled: %v", fa.files)
	}
}

// Unhealthy boot: the target build of a mod crashes the server, so the backup is
// restored, the DB rows are rewritten, and the server is left on the old version.
func TestMigrateRollback(t *testing.T) {
	fa := newFakeAgent("a-1.0.jar")
	fa.brokenFiles["a-2.0.jar"] = true
	source := &fakeSource{byTarget: map[string]map[string][]modrinth.Version{
		"projA": {"1.21.5": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar")}},
	}}
	f := newFixture(t, fa, source)
	modA := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")

	run := f.migrateAndWait(t, "1.21.5")
	if run.Status != "reverted" {
		t.Fatalf("status = %q (%s), want reverted", run.Status, run.Detail)
	}

	ctx := context.Background()
	srv, _ := f.store.GetServer(ctx, f.server.ID)
	if srv.MCVersion != "1.21.4" {
		t.Fatalf("server version = %q, want restored 1.21.4", srv.MCVersion)
	}
	gotA, _ := f.store.GetMod(ctx, modA.ID)
	if gotA.VersionID == nil || *gotA.VersionID != "vA1" || gotA.FileName != "a-1.0.jar" {
		t.Fatalf("modA not restored: %+v", gotA)
	}
	if !fa.hasFile("a-1.0.jar") || fa.hasFile("a-2.0.jar") {
		t.Fatalf("jars not restored from backup: %v", fa.files)
	}
}

// Backup failure aborts the migration before anything changes.
func TestMigrateBackupFailureAborts(t *testing.T) {
	fa := newFakeAgent("a-1.0.jar")
	fa.backupErr = errors.New("disk full")
	source := &fakeSource{byTarget: map[string]map[string][]modrinth.Version{
		"projA": {"1.21.5": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar")}},
	}}
	f := newFixture(t, fa, source)
	modA := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")

	run := f.migrateAndWait(t, "1.21.5")
	if run.Status != "failed" {
		t.Fatalf("status = %q (%s), want failed", run.Status, run.Detail)
	}
	ctx := context.Background()
	srv, _ := f.store.GetServer(ctx, f.server.ID)
	if srv.MCVersion != "1.21.4" {
		t.Fatalf("server version changed despite backup failure: %q", srv.MCVersion)
	}
	gotA, _ := f.store.GetMod(ctx, modA.ID)
	if *gotA.VersionID != "vA1" {
		t.Fatalf("mod changed despite backup failure: %+v", gotA)
	}
	if !fa.hasFile("a-1.0.jar") || fa.hasFile("a-2.0.jar") {
		t.Fatalf("files changed despite backup failure: %v", fa.files)
	}
}

// A second Trigger while a run is in flight is rejected.
func TestMigrateGuardsConcurrentRuns(t *testing.T) {
	fa := newFakeAgent()
	source := &fakeSource{byTarget: map[string]map[string][]modrinth.Version{}}
	f := newFixture(t, fa, source)

	f.engine.mu.Lock()
	f.engine.active[f.server.ID] = true
	f.engine.mu.Unlock()

	if _, err := f.engine.Trigger(context.Background(), f.server.ID, "1.21.5"); err != ErrAlreadyRunning {
		t.Fatalf("Trigger error = %v, want ErrAlreadyRunning", err)
	}
}
