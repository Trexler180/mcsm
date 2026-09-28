package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// A server that will not start must say why, from the panel's own diagnosis.
//
// This is the gap the facade shipped with. ServerManager records exactly what a
// failed boot was missing — the dashboard reads that record and offers a
// one-click install from it — while diagnostics returned only indexed log
// lines, which are empty for a crash during early startup. An agent asked to
// fix such a server therefore saw nothing, inferred a cause, and proposed
// disabling mods the operator had deliberately installed.
func TestDiagnosticsReportWhyAServerCannotStart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	if _, err := e.store.RecordConflict(ctx, e.serverA, "missing_dependency",
		"glitchcore is required but not installed", []string{"glitchcore"}); err != nil {
		t.Fatal(err)
	}

	_, out, err := e.svc.serverDiagnostics(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Blockers) != 1 {
		t.Fatalf("blockers = %#v, want the recorded missing dependency", out.Blockers)
	}
	b := out.Blockers[0]
	if b.Kind != "missing_dependency" {
		t.Errorf("kind = %q", b.Kind)
	}
	if !strings.Contains(b.Summary, "glitchcore") {
		t.Errorf("summary does not name the missing mod: %q", b.Summary)
	}
	if len(b.Mods) != 1 || b.Mods[0] != "glitchcore" {
		t.Errorf("mods = %#v, want the id an install can be driven from", b.Mods)
	}
	// The remedy has to be stated, not left to be inferred — inferring it is what
	// produced "disable the mod that needs the dependency".
	if !strings.Contains(b.Fix, "resolve_missing_dependencies") {
		t.Errorf("fix does not point at the tool that resolves it: %q", b.Fix)
	}
	if strings.Contains(strings.ToLower(b.Fix), "disable the mod that requires") == false &&
		!strings.Contains(b.Fix, "Do not disable") {
		t.Errorf("fix does not warn against disabling the dependent mod: %q", b.Fix)
	}
}

// A resolved blocker is history, not a reason the server is down now.
func TestResolvedBlockersAreNotReported(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	if _, err := e.store.RecordConflict(ctx, e.serverA, "incompatible",
		"2 mod conflict(s) detected", []string{"Async", "Moonrise"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ResolveServerConflicts(ctx, e.serverA); err != nil {
		t.Fatal(err)
	}

	_, out, err := e.svc.serverDiagnostics(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Blockers) != 0 {
		t.Fatalf("a resolved blocker was still reported: %#v", out.Blockers)
	}
}

// Blockers are panel-computed, but the mod names inside them come from the
// failing server's own output, so they go through the same cleaning as evidence.
func TestBlockerTextIsCleaned(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	if _, err := e.store.RecordConflict(ctx, e.serverA, "missing_dependency",
		"glitchcore is required\x00 but not installed\u202e", []string{"glitch\u200bcore"}); err != nil {
		t.Fatal(err)
	}

	_, out, err := e.svc.serverDiagnostics(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Blockers) != 1 {
		t.Fatalf("blockers = %#v", out.Blockers)
	}
	b := out.Blockers[0]
	for _, bad := range []string{"\x00", "\u202e", "\u200b"} {
		if strings.Contains(b.Summary, bad) || strings.Contains(strings.Join(b.Mods, ""), bad) {
			t.Errorf("invisible character survived into a blocker: %q / %#v", b.Summary, b.Mods)
		}
	}
}

// The resolver refuses anything that is not a mod id, so a blocker's contents
// cannot become a free-form lookup.
func TestResolveMissingDependenciesRejectsNonModIDs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	_, _, err := e.svc.resolveMissingDependencies(p)(ctx, nil, resolveMissingDepsInput{
		ServerID: e.serverA,
		ModIDs:   []string{"https://evil.example/x", "../../etc/passwd", ""},
	})
	if err == nil {
		t.Fatal("a URL and a path were accepted as mod ids")
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("%d malformed call(s) reached the backend", len(calls))
	}
}
