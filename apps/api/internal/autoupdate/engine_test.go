package autoupdate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// ── Fakes ────────────────────────────────────────────────────────────

// fakeAgent simulates a node agent: it tracks which jars are "on disk" and
// decides each boot's outcome from whether any broken jar is present.
type fakeAgent struct {
	mu          sync.Mutex
	files       map[string]bool // filename -> present in /mods
	brokenFiles map[string]bool // filename -> presence makes the boot crash
	status      string
	startCalls  int
}

func newFakeAgent(files ...string) *fakeAgent {
	a := &fakeAgent{files: map[string]bool{}, brokenFiles: map[string]bool{}, status: "offline"}
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

func (a *fakeAgent) hasFile(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.files[name]
}

// fakeSource serves canned Modrinth version listings and fake downloads.
type fakeSource struct {
	byProject map[string][]modrinth.Version
	byID      map[string]*modrinth.Version
}

func (f *fakeSource) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	return f.byProject[projectID], nil
}

func (f *fakeSource) GetVersion(ctx context.Context, versionID string) (*modrinth.Version, error) {
	v, ok := f.byID[versionID]
	if !ok {
		return nil, fmt.Errorf("version not found")
	}
	return v, nil
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
	v := modrinth.Version{
		ID:            id,
		ProjectID:     projectID,
		Name:          "v" + number,
		VersionNumber: number,
	}
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
	source *fakeSource
	server *store.Server
}

func newFixture(t *testing.T, agent *fakeAgent, source *fakeSource) *fixture {
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
		store:        s,
		modrinth:     source,
		newAgent:     func(n *store.Node) agentAPI { return agent },
		active:       map[string]bool{},
		pollInterval: time.Millisecond,
		offlineGrace: 50 * time.Millisecond,
		stableFor:    0,
		startTimeout: 2 * time.Second,
		runTimeout:   30 * time.Second,
	}
	return &fixture{store: s, engine: e, agent: agent, source: source, server: srv}
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

// runAndWait triggers a run and polls until it reaches a terminal state.
func (f *fixture) runAndWait(t *testing.T) *store.ModUpdateRun {
	t.Helper()
	ctx := context.Background()
	// The run row turns terminal a moment before the engine releases its
	// per-server lock, so a back-to-back Trigger may briefly see
	// ErrAlreadyRunning; retry instead of flaking.
	var run *store.ModUpdateRun
	var err error
	for i := 0; i < 100; i++ {
		run, err = f.engine.Trigger(ctx, f.server.ID, "manual")
		if err != ErrAlreadyRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := f.store.GetModUpdateRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("GetModUpdateRun: %v", err)
		}
		if got.Status != "running" {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not finish in time")
	return nil
}

// ── PickUpdate ───────────────────────────────────────────────────────

func TestPickUpdate(t *testing.T) {
	versions := []modrinth.Version{ // newest-first, like the Modrinth API
		mkVersion("v3", "p", "3.0", "p-3.0.jar"),
		mkVersion("v2", "p", "2.0", "p-2.0.jar"),
		mkVersion("v1", "p", "1.0", "p-1.0.jar"),
	}
	cases := []struct {
		name    string
		current string
		skipped map[string]bool
		want    string // version ID, "" for nil
	}{
		{"already newest", "v3", nil, ""},
		{"newer available", "v1", nil, "v3"},
		{"newest skipped picks next", "v1", map[string]bool{"v3": true}, "v2"},
		{"all newer skipped", "v1", map[string]bool{"v3": true, "v2": true}, ""},
		{"current unknown picks newest", "vX", nil, "v3"},
		{"empty list", "v1", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := versions
			if tc.name == "empty list" {
				in = nil
			}
			got := PickUpdate(in, tc.current, tc.skipped)
			gotID := ""
			if got != nil {
				gotID = got.ID
			}
			if gotID != tc.want {
				t.Fatalf("PickUpdate = %q, want %q", gotID, tc.want)
			}
		})
	}
}

// ── Engine flows ─────────────────────────────────────────────────────

// Healthy path: the update is applied, the server boots fine, the new version
// sticks.
func TestEngineHealthyUpdate(t *testing.T) {
	agent := newFakeAgent("a-1.0.jar")
	source := &fakeSource{
		byProject: map[string][]modrinth.Version{
			"projA": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar"), mkVersion("vA1", "projA", "1.0", "a-1.0.jar")},
		},
		byID: map[string]*modrinth.Version{},
	}
	f := newFixture(t, agent, source)
	mod := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")

	run := f.runAndWait(t)
	if run.Status != "success" {
		t.Fatalf("run status = %q (%s), want success", run.Status, run.Detail)
	}
	got, err := f.store.GetMod(context.Background(), mod.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionID == nil || *got.VersionID != "vA2" || got.FileName != "a-2.0.jar" {
		t.Fatalf("mod not updated: %+v", got)
	}
	if !agent.hasFile("a-2.0.jar") || agent.hasFile("a-1.0.jar") {
		t.Fatalf("jar not swapped: %v", agent.files)
	}
	skipped, _ := f.store.ListSkippedModVersions(context.Background(), f.server.ID)
	if len(skipped) != 0 {
		t.Fatalf("nothing should be blocklisted, got %+v", skipped)
	}
}

// Broken single update: the server crashes on boot, so the update is reverted,
// the version is blocklisted, and the next run finds nothing to do.
func TestEngineRevertAndSkip(t *testing.T) {
	agent := newFakeAgent("a-1.0.jar")
	agent.brokenFiles["a-2.0.jar"] = true
	oldV := mkVersion("vA1", "projA", "1.0", "a-1.0.jar")
	source := &fakeSource{
		byProject: map[string][]modrinth.Version{
			"projA": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar"), oldV},
		},
		byID: map[string]*modrinth.Version{"vA1": &oldV},
	}
	f := newFixture(t, agent, source)
	mod := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")

	run := f.runAndWait(t)
	if run.Status != "reverted" {
		t.Fatalf("run status = %q (%s), want reverted", run.Status, run.Detail)
	}
	got, err := f.store.GetMod(context.Background(), mod.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionID == nil || *got.VersionID != "vA1" || got.FileName != "a-1.0.jar" {
		t.Fatalf("mod not reverted: %+v", got)
	}
	if !agent.hasFile("a-1.0.jar") || agent.hasFile("a-2.0.jar") {
		t.Fatalf("jar not restored: %v", agent.files)
	}
	skipped, err := f.store.ListSkippedModVersions(context.Background(), f.server.ID)
	if err != nil || len(skipped) != 1 {
		t.Fatalf("want 1 blocklisted version, got %v (%v)", skipped, err)
	}
	if skipped[0].ProjectID != "projA" || skipped[0].VersionID != "vA2" || skipped[0].Reason == "" {
		t.Fatalf("wrong blocklist entry: %+v", skipped[0])
	}
	if !strings.Contains(string(run.Detail), "reverted_skipped") {
		t.Fatalf("detail missing step outcome: %s", run.Detail)
	}

	// The blocklisted version must not be retried.
	run2 := f.runAndWait(t)
	if run2.Status != "no_updates" {
		t.Fatalf("second run status = %q (%s), want no_updates", run2.Status, run2.Detail)
	}
}

// Two updates land together and the boot breaks: everything is rolled back,
// the baseline is verified, then each update is re-applied alone. The good one
// stays, the bad one is reverted and blocklisted.
func TestEngineIsolatesCulprit(t *testing.T) {
	agent := newFakeAgent("a-1.0.jar", "b-1.0.jar")
	agent.brokenFiles["b-2.0.jar"] = true
	oldA := mkVersion("vA1", "projA", "1.0", "a-1.0.jar")
	oldB := mkVersion("vB1", "projB", "1.0", "b-1.0.jar")
	source := &fakeSource{
		byProject: map[string][]modrinth.Version{
			"projA": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar"), oldA},
			"projB": {mkVersion("vB2", "projB", "2.0", "b-2.0.jar"), oldB},
		},
		byID: map[string]*modrinth.Version{"vA1": &oldA, "vB1": &oldB},
	}
	f := newFixture(t, agent, source)
	modA := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")
	modB := f.addMod(t, "projB", "vB1", "1.0", "b-1.0.jar")

	run := f.runAndWait(t)
	if run.Status != "partial" {
		t.Fatalf("run status = %q (%s), want partial", run.Status, run.Detail)
	}

	ctx := context.Background()
	gotA, _ := f.store.GetMod(ctx, modA.ID)
	if gotA.VersionID == nil || *gotA.VersionID != "vA2" {
		t.Fatalf("good update should stick: %+v", gotA)
	}
	gotB, _ := f.store.GetMod(ctx, modB.ID)
	if gotB.VersionID == nil || *gotB.VersionID != "vB1" {
		t.Fatalf("bad update should be reverted: %+v", gotB)
	}
	if !agent.hasFile("a-2.0.jar") || !agent.hasFile("b-1.0.jar") || agent.hasFile("b-2.0.jar") {
		t.Fatalf("wrong final jars: %v", agent.files)
	}

	skipped, _ := f.store.ListSkippedModVersions(ctx, f.server.ID)
	if len(skipped) != 1 || skipped[0].VersionID != "vB2" {
		t.Fatalf("want exactly vB2 blocklisted, got %+v", skipped)
	}
}

// Pinned and disabled mods are never touched.
func TestEngineSkipsPinnedAndDisabled(t *testing.T) {
	agent := newFakeAgent("a-1.0.jar", "b-1.0.jar")
	source := &fakeSource{
		byProject: map[string][]modrinth.Version{
			"projA": {mkVersion("vA2", "projA", "2.0", "a-2.0.jar")},
			"projB": {mkVersion("vB2", "projB", "2.0", "b-2.0.jar")},
		},
		byID: map[string]*modrinth.Version{},
	}
	f := newFixture(t, agent, source)
	ctx := context.Background()
	pinned := f.addMod(t, "projA", "vA1", "1.0", "a-1.0.jar")
	if err := f.store.SetModPinned(ctx, pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	disabled := f.addMod(t, "projB", "vB1", "1.0", "b-1.0.jar")
	if err := f.store.SetModEnabled(ctx, disabled.ID, false, "b-1.0.jar.disabled"); err != nil {
		t.Fatal(err)
	}

	run := f.runAndWait(t)
	if run.Status != "no_updates" {
		t.Fatalf("run status = %q (%s), want no_updates", run.Status, run.Detail)
	}
	if agent.startCalls != 0 {
		t.Fatalf("server should not have been restarted, got %d starts", agent.startCalls)
	}
}

// A second Trigger while a run is in flight is rejected.
func TestTriggerGuardsConcurrentRuns(t *testing.T) {
	agent := newFakeAgent()
	source := &fakeSource{byProject: map[string][]modrinth.Version{}, byID: map[string]*modrinth.Version{}}
	f := newFixture(t, agent, source)

	f.engine.mu.Lock()
	f.engine.active[f.server.ID] = true
	f.engine.mu.Unlock()

	if _, err := f.engine.Trigger(context.Background(), f.server.ID, "manual"); err != ErrAlreadyRunning {
		t.Fatalf("Trigger error = %v, want ErrAlreadyRunning", err)
	}
}
