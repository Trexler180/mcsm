package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

// depFixture is a small server with a library, two mods that need it, one that
// merely likes it, and a hand-uploaded jar nothing can say anything about.
type depFixture struct {
	store *store.Store
	srvID string
	mods  []*store.InstalledMod
	byPID map[string]*store.InstalledMod
}

func newDepFixture(t *testing.T) *depFixture {
	t.Helper()
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

	f := &depFixture{store: s, srvID: srv.ID, byPID: map[string]*store.InstalledMod{}}
	mk := func(pid, name string, asDep bool) *store.InstalledMod {
		p, v := pid, pid+"-v1"
		m, err := s.CreateMod(ctx, &store.InstalledMod{
			ServerID: srv.ID, Source: "modrinth", SourceID: &p, VersionID: &v,
			Name: name, Version: "1.0", FileName: name + ".jar", InstallPath: "/mods",
			InstalledAsDep: asDep,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.mods = append(f.mods, m)
		f.byPID[pid] = m
		return m
	}

	mk("lib", "Fabric API", false)
	mk("mod-a", "Clumps", false)
	mk("mod-b", "Forge Config API Port", false)
	mk("mod-c", "Optional User", false)
	mk("sublib", "Tiny Helper", true) // pulled in by mod-a only

	custom, err := s.CreateMod(ctx, &store.InstalledMod{
		ServerID: srv.ID, Source: "custom", Name: "handmade", Version: "unknown",
		FileName: "handmade.jar", InstallPath: "/mods",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mods = append(f.mods, custom)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.ReplaceModDependencies(ctx, srv.ID, "mod-a", []store.ModDependency{
		{DependencyProjectID: "lib", Type: store.DependencyRequired},
		{DependencyProjectID: "sublib", Type: store.DependencyRequired},
	}))
	must(s.ReplaceModDependencies(ctx, srv.ID, "mod-b", []store.ModDependency{
		{DependencyProjectID: "lib", Type: store.DependencyRequired},
	}))
	must(s.ReplaceModDependencies(ctx, srv.ID, "mod-c", []store.ModDependency{
		{DependencyProjectID: "lib", Type: store.DependencyOptional},
	}))
	return f
}

func (f *depFixture) handlers() *ModHandlers { return &ModHandlers{store: f.store} }

func TestModImpactSeparatesRequiredFromOptional(t *testing.T) {
	ctx := context.Background()
	f := newDepFixture(t)

	impact, err := f.handlers().modImpact(ctx, f.srvID, f.byPID["lib"], f.mods)
	if err != nil {
		t.Fatal(err)
	}
	if !impact.Checked || !impact.Breaking() {
		t.Fatalf("removing the library should be flagged as breaking: %+v", impact)
	}
	if len(impact.Required) != 2 ||
		impact.Required[0].Name != "Clumps" || impact.Required[1].Name != "Forge Config API Port" {
		t.Fatalf("required dependents = %+v, want Clumps and Forge Config API Port (sorted)", impact.Required)
	}
	if len(impact.Optional) != 1 || impact.Optional[0].Name != "Optional User" {
		t.Fatalf("optional dependents = %+v, want just Optional User", impact.Optional)
	}
	// The hand-uploaded jar could declare a dependency we can never see.
	if impact.Unchecked != 1 {
		t.Fatalf("unchecked = %d, want 1 (the custom jar)", impact.Unchecked)
	}

	// Every list has to serialize as [] rather than null: the dialog reads
	// .length off each one, and a null crashes the Mods tab.
	body, err := json.Marshal(impact)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"required":[`, `"optional":[`, `"orphaning":[`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("%s missing or null in %s", field, body)
		}
	}
}

func TestModImpactReportsNewlyOrphanedDependencies(t *testing.T) {
	ctx := context.Background()
	f := newDepFixture(t)

	// Removing mod-a: nothing breaks (nothing requires it), but the helper it
	// pulled in is left with no dependents, while the shared library is not.
	impact, err := f.handlers().modImpact(ctx, f.srvID, f.byPID["mod-a"], f.mods)
	if err != nil {
		t.Fatal(err)
	}
	if impact.Breaking() {
		t.Fatalf("nothing requires mod-a; got %+v", impact.Required)
	}
	if len(impact.Orphaning) != 1 || impact.Orphaning[0] != "Tiny Helper" {
		t.Fatalf("orphaning = %v, want [Tiny Helper] — the shared library is still needed by mod-b", impact.Orphaning)
	}
}

func TestModImpactForUntrackedJarIsUnchecked(t *testing.T) {
	ctx := context.Background()
	f := newDepFixture(t)

	custom := f.mods[len(f.mods)-1]
	impact, err := f.handlers().modImpact(ctx, f.srvID, custom, f.mods)
	if err != nil {
		t.Fatal(err)
	}
	if impact.Checked {
		t.Fatal("a jar with no project identity can't be checked against the graph")
	}
	if impact.Breaking() {
		t.Fatalf("no edges can point at it: %+v", impact.Required)
	}
}

// The guard is the API-level half of the warning: a delete that would break other
// content is refused with the impact attached, and goes through once forced.
func TestCheckDependencyImpactBlocksUnlessForced(t *testing.T) {
	ctx := context.Background()
	f := newDepFixture(t)
	h := f.handlers()

	// Pre-record the scan so the guard doesn't try to reach Modrinth.
	if err := f.store.SetModDependencyScan(ctx, f.srvID, modSetFingerprint(f.mods)); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	if _, ok := h.checkDependencyImpact(rec, req, f.srvID, f.byPID["lib"], false, "removing"); ok {
		t.Fatal("removing a required library should not be allowed without force")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body struct {
		Error  string    `json:"error"`
		Impact ModImpact `json:"impact"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Impact.Required) != 2 {
		t.Fatalf("the refusal should carry the dependents: %+v", body.Impact)
	}

	rec = httptest.NewRecorder()
	impact, ok := h.checkDependencyImpact(rec, httptest.NewRequest(http.MethodDelete, "/", nil), f.srvID, f.byPID["lib"], true, "removing")
	if !ok || impact == nil {
		t.Fatal("force should let the removal proceed")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("the guard should write nothing when it passes, got %d %q", rec.Code, rec.Body.String())
	}

	// A mod nothing requires passes without force.
	rec = httptest.NewRecorder()
	if _, ok := h.checkDependencyImpact(rec, httptest.NewRequest(http.MethodDelete, "/", nil), f.srvID, f.byPID["mod-b"], false, "removing"); !ok {
		t.Fatalf("nothing requires mod-b; removal should pass, got %d %q", rec.Code, rec.Body.String())
	}
}

// A stale edge from a version that no longer declares the dependency must stop
// counting, which is the whole point of replacing rather than accumulating.
func TestReplaceModDependenciesDropsStaleEdges(t *testing.T) {
	ctx := context.Background()
	f := newDepFixture(t)

	if err := f.store.ReplaceModDependencies(ctx, f.srvID, "mod-a", nil); err != nil {
		t.Fatal(err)
	}
	impact, err := f.handlers().modImpact(ctx, f.srvID, f.byPID["lib"], f.mods)
	if err != nil {
		t.Fatal(err)
	}
	if len(impact.Required) != 1 || impact.Required[0].Name != "Forge Config API Port" {
		t.Fatalf("required = %+v, want only Forge Config API Port after mod-a dropped its dependency", impact.Required)
	}
}

func TestDeclaredDepsKeepsOnlyMeaningfulKinds(t *testing.T) {
	deps := declaredDeps(modrinth.Version{Dependencies: []modrinth.Dependency{
		{ProjectID: "a", DependencyType: "required"},
		{ProjectID: "b", DependencyType: "optional"},
		{ProjectID: "c", DependencyType: "incompatible"},
		{ProjectID: "d", DependencyType: "embedded"},
		{ProjectID: "", DependencyType: "required"},
	}})
	if len(deps) != 2 {
		t.Fatalf("deps = %+v, want required a and optional b only", deps)
	}
	if deps[0].DependencyProjectID != "a" || deps[0].Type != store.DependencyRequired {
		t.Fatalf("first edge = %+v", deps[0])
	}
	if deps[1].DependencyProjectID != "b" || deps[1].Type != store.DependencyOptional {
		t.Fatalf("second edge = %+v", deps[1])
	}
}

// The fingerprint gates the upstream refresh: it must ignore ordering but notice
// a version change, or the graph either never refreshes or refreshes constantly.
func TestModSetFingerprint(t *testing.T) {
	pid, v1, v2 := "p", "v1", "v2"
	a := &store.InstalledMod{Source: "modrinth", SourceID: &pid, VersionID: &v1}
	other := "q"
	b := &store.InstalledMod{Source: "modrinth", SourceID: &other, VersionID: &v1}

	if modSetFingerprint([]*store.InstalledMod{a, b}) != modSetFingerprint([]*store.InstalledMod{b, a}) {
		t.Fatal("order must not change the fingerprint")
	}
	updated := &store.InstalledMod{Source: "modrinth", SourceID: &pid, VersionID: &v2}
	if modSetFingerprint([]*store.InstalledMod{a}) == modSetFingerprint([]*store.InstalledMod{updated}) {
		t.Fatal("a version change must change the fingerprint")
	}
}
