package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/store"
)

var errBackendUnavailable = errors.New("the mod source refused the request")

func TestOperatorOutputHasConcreteClaudeCompatibleSchema(t *testing.T) {
	schema, err := jsonschema.For[operatorOutput](nil)
	if err != nil {
		t.Fatal(err)
	}
	result := schema.Properties["result"]
	if result == nil || result.Type != "string" {
		t.Fatalf("result schema = %#v, want a concrete string type", result)
	}
}

// ── Direct lifecycle ─────────────────────────────────────────────

// The operator lifecycle tools execute against the node on the call itself.
// That is the whole difference from request_server_action, so it is asserted
// directly: one node call, and nothing parked in the approval queue.
func TestDirectLifecycleExecutesWithoutApproval(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		action     string
		scope      store.MCPScope
		permission store.ServerPermission
		count      func(starts, stops, restarts int) int
	}{
		{"start", store.MCPScopePowerStart, store.ServerPermissionPowerStart,
			func(s, _, _ int) int { return s }},
		{"stop", store.MCPScopePowerStop, store.ServerPermissionPowerStop,
			func(_, s, _ int) int { return s }},
		{"restart", store.MCPScopePowerRestart, store.ServerPermissionPowerRestart,
			func(_, _, r int) int { return r }},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			e := newEnv(t)
			p := principalFor(e.grant)

			_, out, err := e.svc.directLifecycle(p, tc.action, tc.scope, tc.permission)(
				ctx, nil, serverIDInput{ServerID: e.serverA})
			if err != nil {
				t.Fatalf("%s: %v", tc.action, err)
			}
			if out.Action != tc.action || out.Status != "accepted" {
				t.Fatalf("unexpected output %+v", out)
			}
			if got := tc.count(e.node.counts()); got != 1 {
				t.Fatalf("expected exactly one %s on the node, got %d", tc.action, got)
			}

			// Nothing was filed for a human to approve: this path does not use
			// the approval queue at all.
			queued, err := e.store.ListMCPActionRequestsForUser(ctx, e.ownerID, 50)
			if err != nil {
				t.Fatal(err)
			}
			if len(queued) != 0 {
				t.Fatalf("direct lifecycle filed %d approval request(s)", len(queued))
			}
		})
	}
}

// A direct lifecycle call is attributed to the human who delegated it *and* to
// the delegation that carried it, so the trail never reads as an ordinary
// dashboard action by that user.
func TestDirectLifecycleIsAuditedToTheGrant(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)
	p.IP = "203.0.113.9"

	if _, _, err := e.svc.directLifecycle(p, "restart", store.MCPScopePowerRestart, store.ServerPermissionPowerRestart)(
		ctx, nil, serverIDInput{ServerID: e.serverA}); err != nil {
		t.Fatal(err)
	}

	entries, err := e.store.ListAudit(ctx, e.serverA, 50)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Action != "server.restart" {
			continue
		}
		found = true
		if entry.UserID == nil || *entry.UserID != e.ownerID {
			t.Error("restart was not attributed to the delegating human")
		}
		if entry.MCPGrantID == nil || *entry.MCPGrantID != e.grant.ID {
			t.Error("restart was not attributed to the delegation that carried it")
		}
		if entry.IPAddress == nil || *entry.IPAddress != "203.0.113.9" {
			t.Error("the caller IP was not recorded")
		}
	}
	if !found {
		t.Fatal("no server.restart audit entry was written")
	}
}

// Each lifecycle tool is pinned to its own scope and its own exact power leaf,
// which is what keeps the three from collapsing into one "power" capability.
func TestDirectLifecycleDoesNotEscalateAcrossActions(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	startOnly := e.mkGrant(t, e.ownerID, []string{e.serverA}, string(store.MCPScopePowerStart))
	p := principalFor(startOnly)
	// The token claims everything; only the stored grant counts.
	p.Scopes = store.AllMCPScopes()

	if _, _, err := e.svc.directLifecycle(p, "stop", store.MCPScopePowerStop, store.ServerPermissionPowerStop)(
		ctx, nil, serverIDInput{ServerID: e.serverA}); err == nil {
		t.Error("a start-only grant was allowed to stop a server")
	}
	if _, _, err := e.svc.directLifecycle(p, "restart", store.MCPScopePowerRestart, store.ServerPermissionPowerRestart)(
		ctx, nil, serverIDInput{ServerID: e.serverA}); err == nil {
		t.Error("a start-only grant was allowed to restart a server")
	}
	if _, _, err := e.svc.directLifecycle(p, "start", store.MCPScopePowerStart, store.ServerPermissionPowerStart)(
		ctx, nil, serverIDInput{ServerID: e.serverA}); err != nil {
		t.Fatalf("the grant's real authority was refused: %v", err)
	}
	if starts, stops, restarts := e.node.counts(); starts != 1 || stops != 0 || restarts != 0 {
		t.Fatalf("node saw starts=%d stops=%d restarts=%d", starts, stops, restarts)
	}
}

// Direct power scopes must not open the approval-gated tool's own door, and
// holding only the legacy request scope must not confer direct execution.
// The two paths are separate capabilities in both directions.
func TestDirectPowerAndActionRequestAreSeparateCapabilities(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// Direct authority only: request_server_action must still refuse.
	direct := e.mkGrant(t, e.ownerID, []string{e.serverA},
		string(store.MCPScopePowerStart), string(store.MCPScopePowerStop), string(store.MCPScopePowerRestart))
	dp := principalFor(direct)
	dp.Scopes = store.AllMCPScopes()
	if _, _, err := e.svc.requestServerAction(dp)(ctx, nil,
		requestActionInput{ServerID: e.serverA, Action: "restart", Reason: "because"}); err == nil {
		t.Error("direct power scopes were accepted as the action-request capability")
	}

	// Legacy request authority only: no direct lifecycle tool may execute.
	legacy := e.mkGrant(t, e.ownerID, []string{e.serverA}, string(store.MCPScopeActionsRequest))
	lp := principalFor(legacy)
	lp.Scopes = store.AllMCPScopes()
	for _, tc := range []struct {
		action     string
		scope      store.MCPScope
		permission store.ServerPermission
	}{
		{"start", store.MCPScopePowerStart, store.ServerPermissionPowerStart},
		{"stop", store.MCPScopePowerStop, store.ServerPermissionPowerStop},
		{"restart", store.MCPScopePowerRestart, store.ServerPermissionPowerRestart},
	} {
		if _, _, err := e.svc.directLifecycle(lp, tc.action, tc.scope, tc.permission)(
			ctx, nil, serverIDInput{ServerID: e.serverA}); err == nil {
			t.Errorf("the approval-only scope was allowed to %s directly", tc.action)
		}
	}
	if starts, stops, restarts := e.node.counts(); starts+stops+restarts != 0 {
		t.Fatal("a refused call still reached a node")
	}
}

// ── Backend dispatch ─────────────────────────────────────────────

// Every operator tool dispatches a fixed operation name from a closed set. The
// tool chooses the operation; the model never names it. That is the property
// keeping the in-process adapter from becoming a call-anything bridge.
func TestOperatorToolsDispatchFixedOperations(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		operation string
		modID     string
		call      func(e *env, p *Principal) error
	}{
		{"list_server_mods", "mods.list", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.listServerMods(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
			return err
		}},
		{"list_mod_updates", "mods.updates", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.listModUpdates(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
			return err
		}},
		{"find_compatible_mods", "mods.search", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.findCompatibleMods(p)(ctx, nil, modSearchInput{ServerID: e.serverA, Name: "worldedit"})
			return err
		}},
		{"install_mod", "mods.install", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.installMod(p)(ctx, nil, installModInput{ServerID: e.serverA, ProjectID: "abc123"})
			return err
		}},
		{"update_mod", "mods.update", "mod-1", func(e *env, p *Principal) error {
			_, _, err := e.svc.updateMod(p)(ctx, nil, updateModInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		}},
		{"set_mod_enabled", "mods.enabled", "mod-1", func(e *env, p *Principal) error {
			_, _, err := e.svc.setModEnabled(p)(ctx, nil, setModEnabledInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		}},
		{"remove_mod", "mods.remove", "mod-1", func(e *env, p *Principal) error {
			_, _, err := e.svc.removeMod(p)(ctx, nil, removeModInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		}},
		{"create_server_backup", "backups.create", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.createServerBackup(p)(ctx, confirmedBackupRequest(mintBackupConfirmation(ctx, e, p, e.serverA), "accept", true), createBackupInput{ServerID: e.serverA})
			return err
		}},
		{"list_server_backups", "backups.list", "", func(e *env, p *Principal) error {
			_, _, err := e.svc.listServerBackups(p)(ctx, nil, serverIDInput{ServerID: e.serverA})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			p := principalFor(e.grant)
			p.IP = "203.0.113.9"

			if err := tc.call(e, p); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			call := e.operator.last(t)
			if call.operation != tc.operation {
				t.Errorf("expected operation %q, got %q", tc.operation, call.operation)
			}
			if call.serverID != e.serverA {
				t.Errorf("operation ran against the wrong server: %q", call.serverID)
			}
			if call.modID != tc.modID {
				t.Errorf("expected mod id %q, got %q", tc.modID, call.modID)
			}
			// Attribution travels with the operation; authority never does.
			if call.actor.UserID != e.ownerID || call.actor.GrantID != e.grant.ID {
				t.Errorf("actor was not the delegating human/grant: %+v", call.actor)
			}
			if call.actor.IP != "203.0.113.9" {
				t.Errorf("actor IP was not carried: %q", call.actor.IP)
			}
		})
	}
}

// The search tool derives platform and version from the *stored* server, so a
// caller cannot steer compatibility filtering.
func TestFindCompatibleModsDerivesCompatibilityFromTheServer(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	if _, _, err := e.svc.findCompatibleMods(principalFor(e.grant))(ctx, nil,
		modSearchInput{ServerID: e.serverA, Name: "worldedit", Limit: 500}); err != nil {
		t.Fatal(err)
	}
	args := e.operator.last(t).args
	if args["loader"] != "paper" || args["mc_version"] != "1.21.4" {
		t.Errorf("compatibility was not taken from the stored server: %+v", args)
	}
	if args["project_type"] != "plugin" {
		t.Errorf("a paper server should search plugins, got %v", args["project_type"])
	}
	if args["source"] != "modrinth" {
		t.Errorf("source should default to modrinth, got %v", args["source"])
	}
	if limit, _ := args["limit"].(int); limit != 20 {
		t.Errorf("an out-of-range limit should clamp to 20, got %v", args["limit"])
	}
}

// Each mod tool is pinned to its own scope and exact leaf permission. A
// read-only mods grant may list, but not install, update, disable, or remove —
// and no refused call may reach the backend at all.
func TestModToolsDoNotEscalateFromRead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	readOnly := e.mkGrant(t, e.ownerID, []string{e.serverA}, string(store.MCPScopeModsRead))
	p := principalFor(readOnly)
	p.Scopes = store.AllMCPScopes()

	if _, _, err := e.svc.listServerMods(p)(ctx, nil, serverIDInput{ServerID: e.serverA}); err != nil {
		t.Fatalf("mods.read should permit listing: %v", err)
	}
	before := len(e.operator.recorded())

	mutations := map[string]func() error{
		"install": func() error {
			_, _, err := e.svc.installMod(p)(ctx, nil, installModInput{ServerID: e.serverA, ProjectID: "abc123"})
			return err
		},
		"update": func() error {
			_, _, err := e.svc.updateMod(p)(ctx, nil, updateModInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		},
		"set_enabled": func() error {
			_, _, err := e.svc.setModEnabled(p)(ctx, nil, setModEnabledInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		},
		"remove": func() error {
			_, _, err := e.svc.removeMod(p)(ctx, nil, removeModInput{ServerID: e.serverA, ModID: "mod-1"})
			return err
		},
		"backup": func() error {
			_, _, err := e.svc.createServerBackup(p)(ctx, nil, createBackupInput{ServerID: e.serverA})
			return err
		},
	}
	for name, mutate := range mutations {
		if err := mutate(); err == nil {
			t.Errorf("a mods.read-only grant was allowed to %s", name)
		}
	}
	if after := len(e.operator.recorded()); after != before {
		t.Fatalf("%d refused mutation(s) still reached the backend", after-before)
	}
}

// Operator tools stay unregistered when no backend is wired, rather than being
// advertised and failing at call time.
func TestOperatorToolsAreAbsentWithoutABackend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.operator = nil

	res, err := connect(t, e.svc, principalFor(e.grant)).ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	backed := map[string]bool{
		"list_server_mods": true, "find_compatible_mods": true, "list_mod_updates": true,
		"install_mod": true, "update_mod": true, "set_mod_enabled": true,
		"remove_mod": true, "create_server_backup": true, "list_server_backups": true,
	}
	for _, tool := range res.Tools {
		if backed[tool.Name] {
			t.Errorf("tool %q was advertised with no operator backend", tool.Name)
		}
	}
}

// The mod source is a closed enumeration, not a caller-supplied destination.
func TestOperatorSourceIsAClosedSet(t *testing.T) {
	for _, in := range []string{"", "modrinth", "MODRINTH", " curseforge ", "hangar", "spigotmc"} {
		if _, err := operatorSource(in); err != nil {
			t.Errorf("approved source %q was refused: %v", in, err)
		}
	}
	for _, in := range []string{"https://evil.example.com/x.jar", "file:///etc/passwd", "github", "../modrinth"} {
		if _, err := operatorSource(in); err == nil {
			t.Errorf("unapproved source %q was accepted", in)
		}
	}
}

// Ids reaching the backend are opaque tokens, never paths or URLs.
func TestOperatorIDsRejectPathAndURLShapes(t *testing.T) {
	for _, id := range []string{"abc123", "AB-cd_9", "modrinth:P7dR8mSH", "1.21.4"} {
		if !validOperatorID(id) {
			t.Errorf("legitimate id %q was rejected", id)
		}
	}
	for _, id := range []string{"", "../../etc/passwd", "a/b", "https://x.example", "a b", strings.Repeat("a", 161)} {
		if validOperatorID(id) {
			t.Errorf("escape-hatch shaped id %q was accepted", id)
		}
	}
}

// A malformed id is refused before the backend is touched.
func TestMalformedIDsNeverReachTheBackend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	if _, _, err := e.svc.installMod(p)(ctx, nil,
		installModInput{ServerID: e.serverA, ProjectID: "../../etc/passwd"}); err == nil {
		t.Error("a path-shaped project id was accepted")
	}
	if _, _, err := e.svc.updateMod(p)(ctx, nil,
		updateModInput{ServerID: e.serverA, ModID: "mod-1", VersionID: "https://evil.example"}); err == nil {
		t.Error("a URL-shaped version id was accepted")
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("%d malformed call(s) reached the backend", len(calls))
	}
}

// mintBackupConfirmation runs the first half of the elicitation round trip and
// returns the state the server issued for it. Minting never reaches the
// operator backend, so a caller counting backend calls can use it freely.
func mintBackupConfirmation(ctx context.Context, e *env, p *Principal, serverID string) string {
	result, _, err := e.svc.createServerBackup(p)(ctx,
		&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, createBackupInput{ServerID: serverID})
	if err != nil || result == nil {
		return ""
	}
	return result.RequestState
}

// confirmedBackupRequest replays a confirmation the server actually issued.
// The state is no longer derivable from the grant and server, which is the
// point of it: a caller has to have been handed one.
func confirmedBackupRequest(state, action string, confirmed bool) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		InputResponses: mcp.InputResponseMap{
			backupConfirmationID: &mcp.ElicitResult{
				Action:  action,
				Content: map[string]any{"confirmed": confirmed},
			},
		},
		RequestState: state,
	}}
}

func TestBackupCreationRequiresClientMediatedHumanConfirmation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)
	handler := e.svc.createServerBackup(p)
	input := createBackupInput{ServerID: e.serverA}

	result, _, err := handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, input)
	if err != nil {
		t.Fatalf("confirmation request failed: %v", err)
	}
	if result == nil || len(result.InputRequests) != 1 || result.RequestState == "" {
		t.Fatalf("confirmation request = %#v", result)
	}
	state := result.RequestState
	elicit, ok := result.InputRequests[backupConfirmationID].(*mcp.ElicitParams)
	if !ok || elicit.Mode != "form" || !strings.Contains(elicit.Message, "alpha") {
		t.Fatalf("backup elicitation = %#v", result.InputRequests[backupConfirmationID])
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("%d backup call(s) reached the backend before confirmation", len(calls))
	}

	refusals := []*mcp.CallToolRequest{
		confirmedBackupRequest(state, "decline", true),
		confirmedBackupRequest(state, "cancel", true),
		confirmedBackupRequest(state, "accept", false),
		{Params: &mcp.CallToolParamsRaw{
			InputResponses: mcp.InputResponseMap{
				backupConfirmationID: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}},
			},
			RequestState: "confirmation-for-another-request",
		}},
	}
	for i, req := range refusals {
		if _, _, err := handler(ctx, req, input); err == nil {
			t.Errorf("refusal case %d started a backup", i)
		}
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("%d unconfirmed backup call(s) reached the backend", len(calls))
	}

	if _, _, err := handler(ctx, confirmedBackupRequest(state, "accept", true), input); err != nil {
		t.Fatalf("human-confirmed backup was refused: %v", err)
	}
	if calls := e.operator.recorded(); len(calls) != 1 || calls[0].operation != "backups.create" {
		t.Fatalf("human confirmation produced calls %#v", calls)
	}

	entries, err := e.store.ListAudit(ctx, e.serverA, 50)
	if err != nil {
		t.Fatal(err)
	}
	var confirmationAudit *store.AuditEntry
	for _, entry := range entries {
		if entry.Action == "backup.user_confirmed" {
			confirmationAudit = entry
			break
		}
	}
	if confirmationAudit == nil || confirmationAudit.Detail == nil ||
		!strings.Contains(*confirmationAudit.Detail, "mcp_elicitation") {
		t.Fatalf("human backup confirmation was not recorded: %#v", confirmationAudit)
	}
	if confirmationAudit.MCPGrantID == nil || *confirmationAudit.MCPGrantID != e.grant.ID {
		t.Fatal("backup confirmation was not attributed to the MCP grant")
	}
}

// A backend failure is reported as a bounded tool error, never as success.
func TestOperatorBackendFailureIsBoundedAndNotSilent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.operator.fail = errBackendUnavailable

	p := principalFor(e.grant)
	_, _, err := e.svc.createServerBackup(p)(ctx, confirmedBackupRequest(mintBackupConfirmation(ctx, e, p, e.serverA), "accept", true), createBackupInput{ServerID: e.serverA})
	if err == nil {
		t.Fatal("a backend failure was reported as success")
	}
	if len(err.Error()) > 300 {
		t.Fatalf("tool error was not bounded: %d chars", len(err.Error()))
	}
}
