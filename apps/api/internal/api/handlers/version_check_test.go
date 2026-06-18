package handlers

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// fakeSource implements sourceClient for classifyForTarget tests. Only
// GetVersions is exercised; the rest satisfy the interface.
type fakeSource struct {
	versions []modrinth.Version
	err      error
}

func (f *fakeSource) Search(context.Context, modrinth.SearchParams) (*modrinth.SearchResult, error) {
	return nil, nil
}
func (f *fakeSource) GetProject(context.Context, string) (*modrinth.Project, error) {
	return nil, nil
}
func (f *fakeSource) GetVersions(context.Context, string, string, string) ([]modrinth.Version, error) {
	return f.versions, f.err
}

func ver(id, num, fileURL string) modrinth.Version {
	v := modrinth.Version{ID: id, VersionNumber: num}
	if fileURL != "" {
		f := modrinth.VersionFile{URL: fileURL, Filename: num + ".jar", Primary: true}
		v.Files = []modrinth.VersionFile{f}
	}
	return v
}

func TestClassifyForTarget(t *testing.T) {
	tests := []struct {
		name       string
		src        *fakeSource
		current    string
		wantStatus string
		wantTarget string
	}{
		{
			name:       "no builds for target is incompatible",
			src:        &fakeSource{versions: nil},
			current:    "v-old",
			wantStatus: compatIncompatible,
		},
		{
			name:       "current build already supports target",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "http://x"), ver("v-cur", "1.0", "http://x")}},
			current:    "v-cur",
			wantStatus: compatSupported,
		},
		{
			name:       "newer downloadable build is a compatible move",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "http://x"), ver("v-mid", "1.5", "http://x")}},
			current:    "v-cur",
			wantStatus: compatCompatible,
			wantTarget: "v-new",
		},
		{
			name:       "builds exist but none downloadable is incompatible",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "")}},
			current:    "v-cur",
			wantStatus: compatIncompatible,
		},
		{
			name:       "upstream error is unknown",
			src:        &fakeSource{err: errors.New("boom")},
			current:    "v-cur",
			wantStatus: compatUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := modCompat{}
			got := classifyForTarget(context.Background(), tc.src, "proj", tc.current, "fabric", "1.21", &out)
			if got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if tc.wantTarget != "" && out.TargetVersionID != tc.wantTarget {
				t.Fatalf("target version id = %q, want %q", out.TargetVersionID, tc.wantTarget)
			}
		})
	}
}

func depTestStore(t *testing.T) *store.Store {
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

// A mod that migrates fine but whose required dependency will be disabled gets a
// DepWarning; the disabled dependency and unrelated mods do not.
func TestAnnotateDepWarnings(t *testing.T) {
	ctx := context.Background()
	s := depTestStore(t)

	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "o@e.com", "h", "user")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := s.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: user.ID, Name: "smp", Platform: "fabric", MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25565})
	if err != nil {
		t.Fatal(err)
	}

	mk := func(pid, name string) *store.InstalledMod {
		p, v := pid, pid+"-v1"
		m, err := s.CreateMod(ctx, &store.InstalledMod{ServerID: srv.ID, Source: "modrinth", SourceID: &p, VersionID: &v, Name: name, Version: "1.0", FileName: name + ".jar", Enabled: true, InstallPath: "/mods"})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	dependent := mk("pidA", "Cool Mod")   // compatible, requires the lib
	lib := mk("pidB", "Some Library")     // incompatible → will be disabled
	loner := mk("pidC", "Standalone Mod") // compatible, no deps
	if err := s.AddModDependency(ctx, srv.ID, "pidA", "pidB"); err != nil {
		t.Fatal(err)
	}

	mods := []*store.InstalledMod{dependent, lib, loner}
	results := []modCompat{
		{ModID: dependent.ID, Status: compatCompatible},
		{ModID: lib.ID, Status: compatIncompatible},
		{ModID: loner.ID, Status: compatCompatible},
	}

	annotateDepWarnings(ctx, s, srv.ID, mods, results)

	if len(results[0].DepWarnings) != 1 || results[0].DepWarnings[0] != "Some Library" {
		t.Fatalf("dependent should warn about Some Library, got %v", results[0].DepWarnings)
	}
	if len(results[1].DepWarnings) != 0 {
		t.Fatalf("the disabled dependency should not warn, got %v", results[1].DepWarnings)
	}
	if len(results[2].DepWarnings) != 0 {
		t.Fatalf("the standalone mod should not warn, got %v", results[2].DepWarnings)
	}
}
