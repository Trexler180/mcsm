package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

type fakeMigrationStarter struct {
	calls    int
	serverID string
	target   string
}

func (f *fakeMigrationStarter) Trigger(_ context.Context, serverID, target string) (*store.VersionMigration, error) {
	f.calls++
	f.serverID, f.target = serverID, target
	return &store.VersionMigration{ID: "run-1", ServerID: serverID, ToMCVersion: target}, nil
}

func TestVersionUpgradeRequiresDashboardApprovalAndExecutesOnce(t *testing.T) {
	e := newEnv(t)
	fake := &fakeMigrationStarter{}
	e.svc.migrations = fake
	p := principalFor(e.grant)

	_, out, err := e.svc.requestVersionUpgrade(p)(context.Background(), nil, versionUpgradeInput{
		ServerID: e.serverA, TargetVersion: "1.21.5", Reason: "target-version compatibility was reviewed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.MCPActionPending {
		t.Fatalf("status = %q", out.Status)
	}
	if fake.calls != 0 {
		t.Fatal("migration ran before human approval")
	}

	if _, err := e.svc.ExecuteApprovedAction(context.Background(), out.RequestID, e.ownerID, e.ownerID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.serverID != e.serverA || fake.target != "1.21.5" {
		t.Fatalf("migration calls=%d server=%q target=%q", fake.calls, fake.serverID, fake.target)
	}
	if _, err := e.svc.ExecuteApprovedAction(context.Background(), out.RequestID, e.ownerID, e.ownerID, "127.0.0.1"); err != ErrActionNotPending {
		t.Fatalf("second approval error = %v, want ErrActionNotPending", err)
	}
	if fake.calls != 1 {
		t.Fatalf("migration executed %d times", fake.calls)
	}
}

func TestPlanVersionUpgradeIsReadOnly(t *testing.T) {
	e := newEnv(t)
	p := principalFor(e.grant)
	_, _, err := e.svc.planVersionUpgrade(p)(context.Background(), nil, versionUpgradeInput{
		ServerID: e.serverA, TargetVersion: "1.21.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	call := e.operator.last(t)
	if call.operation != "mods.version_check" || call.args["mc_version"] != "1.21.5" {
		t.Fatalf("unexpected plan call %+v", call)
	}
	requests, err := e.store.ListMCPActionRequestsForUser(context.Background(), e.ownerID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("read-only plan filed %d action requests", len(requests))
	}
}

// A migration record is not panel-authored prose. Its per-mod `error` and its
// `message` are raw Go error strings from the node and the download path, and
// its mod names come from the jars. Marshalling the struct straight to the
// model skipped the sanitizer entirely, because sanitizeOperatorResult only
// walks strings, slices, and maps — a typed struct fell through untouched.
func TestUpgradeStatusIsSanitizedLikeEveryOtherResult(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	detail := []byte(`{
		"phase": "restoring",
		"message": "boot failed: dial https://node.internal:8443 password=hunter2 — restoring",
		"mods": [{"mod_id": "m1", "name": "Bad\u202eMod", "error": "token=mcsm_pat_supersecretvalue rejected"}]
	}`)
	run, err := e.store.CreateVersionMigration(ctx, e.serverA, "1.21.4", "1.21.5", detail)
	if err != nil {
		t.Fatal(err)
	}

	_, out, err := e.svc.getVersionUpgrade(principalFor(e.grant))(ctx, nil, versionUpgradeStatusInput{
		ServerID: e.serverA, RunID: run.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The bidi override is checked in its escaped spelling as well as its raw
	// one: this is a JSON string, and json.Marshal emits U+202E as `\u202e`, so
	// a check for the character alone would pass whether or not it survived.
	for _, leaked := range []string{"hunter2", "mcsm_pat_supersecretvalue", "\u202e", `\u202e`} {
		if strings.Contains(out.Result, leaked) {
			t.Errorf("upgrade status leaked %q:\n%s", leaked, out.Result)
		}
	}
	// The record is still readable — sanitizing must not empty it out.
	if !strings.Contains(out.Result, "restoring") {
		t.Errorf("upgrade status lost its phase:\n%s", out.Result)
	}
}

// The newest-run branch reads through the same path, so it gets the same
// treatment. It is the one an agent actually calls, because the tool documents
// an empty run_id as "the newest run".
func TestNewestUpgradeRunIsSanitizedToo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	if _, err := e.store.CreateVersionMigration(ctx, e.serverA, "1.21.4", "1.21.5",
		[]byte(`{"phase":"applying","message":"api_key: sk-abcdefghijklmnopqrstuvwxyz012345"}`)); err != nil {
		t.Fatal(err)
	}

	_, out, err := e.svc.getVersionUpgrade(principalFor(e.grant))(ctx, nil, versionUpgradeStatusInput{
		ServerID: e.serverA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Result, "sk-abcdefghijklmnopqrstuvwxyz012345") {
		t.Errorf("newest upgrade run leaked a credential:\n%s", out.Result)
	}
}
