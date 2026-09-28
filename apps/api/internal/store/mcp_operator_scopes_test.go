package store

import (
	"errors"
	"strings"
	"testing"
)

// The operator vocabulary is a fixed list. This pins the exact set so adding a
// capability is a deliberate edit here rather than a quiet widening, and it
// fails in both directions.
func TestOperatorScopeVocabularyIsExact(t *testing.T) {
	want := map[string]bool{
		// Diagnostics — read-only.
		"mcp:servers.read":     true,
		"mcp:diagnostics.read": true,
		"mcp:logs.read":        true,
		"mcp:metrics.read":     true,
		"mcp:audit.read":       true,
		// Approval-gated lifecycle.
		"mcp:actions.request": true,
		// Operator — execute directly.
		"mcp:power.start":    true,
		"mcp:power.stop":     true,
		"mcp:power.restart":  true,
		"mcp:mods.read":      true,
		"mcp:mods.install":   true,
		"mcp:mods.update":    true,
		"mcp:mods.remove":    true,
		"mcp:backups.create": true,
		// Player and console writes. `mcp:console.run` is bounded to the closed
		// verb table in mcpserver/console.go — it is not arbitrary console access,
		// and it must never be widened into it.
		"mcp:players.whitelist": true,
		"mcp:console.run":       true,
		// Reads added after an agent proved unable to diagnose a crash: the
		// indexed log events are empty for a failure during early startup, so
		// without the raw text there was nothing to reason from. Each is bounded
		// the same way console.run is — by a closed set rather than a parameter.
		// `mcp:logs.raw` reaches two fixed files, `mcp:config.read` reaches one,
		// and no tool under either accepts a path.
		"mcp:logs.raw":     true,
		"mcp:config.read":  true,
		"mcp:players.read": true,
	}

	got := map[string]bool{}
	for _, scope := range AllMCPScopes() {
		if got[scope] {
			t.Errorf("scope %q is advertised twice", scope)
		}
		got[scope] = true
	}
	for scope := range want {
		if !got[scope] {
			t.Errorf("expected scope %q is not advertised", scope)
		}
	}
	for scope := range got {
		if !want[scope] {
			t.Errorf("unexpected scope %q is advertised", scope)
		}
	}
}

// Every operator scope maps to the *exact* leaf permission its tool needs, not
// to a broader group. A mutation scope satisfied by group access would let
// membership in a coarse role stand in for the specific authority the human
// ticked, which is the escalation this table exists to prevent.
func TestMutationScopesRequireExactLeafPermissions(t *testing.T) {
	cases := []struct {
		scope      MCPScope
		permission ServerPermission
	}{
		{MCPScopePowerStart, ServerPermissionPowerStart},
		{MCPScopePowerStop, ServerPermissionPowerStop},
		{MCPScopePowerRestart, ServerPermissionPowerRestart},
		{MCPScopeModsInstall, ServerPermissionModsInstall},
		{MCPScopeModsUpdate, ServerPermissionModsUpdate},
		{MCPScopeModsRemove, ServerPermissionModsRemove},
		{MCPScopeBackupsCreate, ServerPermissionBackupsCreate},
		{MCPScopePlayersWhitelist, ServerPermissionPlayersWhitelist},
		// console.run consents at the console permission. That is the floor, not
		// the whole check: a verb whose effect needs a narrower leaf is checked
		// against that leaf again in mcpserver. It still must not be satisfiable
		// by group access.
		{MCPScopeConsoleRun, ServerPermissionConsole},
	}
	for _, tc := range cases {
		permission, group, ok := MCPScopeRequirement(string(tc.scope))
		if !ok {
			t.Errorf("%s has no permission requirement", tc.scope)
			continue
		}
		if permission != tc.permission {
			t.Errorf("%s requires %q, want %q", tc.scope, permission, tc.permission)
		}
		if group {
			t.Errorf("%s is satisfied by group access; a mutation must need its exact leaf", tc.scope)
		}
	}
}

// Read scopes never require more than view, so ticking a diagnostic capability
// can never demand — or imply — mutation authority.
func TestDiagnosticScopesOnlyRequireView(t *testing.T) {
	for _, scope := range []MCPScope{
		MCPScopeServersRead, MCPScopeDiagnosticsRead, MCPScopeLogsRead,
		MCPScopeMetricsRead, MCPScopeAuditRead,
	} {
		permission, group, ok := MCPScopeRequirement(string(scope))
		if !ok {
			t.Errorf("%s has no permission requirement", scope)
			continue
		}
		if permission != ServerPermissionView || group {
			t.Errorf("%s requires %q (group=%v), want view", scope, permission, group)
		}
	}
}

// The legacy request scope stays approval-only: it maps to group power access
// because the exact leaf depends on the action, which is re-checked when the
// request is filed and again when a human approves it. It must never be
// confused with the direct power scopes.
func TestActionRequestScopeRemainsApprovalOnly(t *testing.T) {
	permission, group, ok := MCPScopeRequirement(string(MCPScopeActionsRequest))
	if !ok {
		t.Fatal("mcp:actions.request has no permission requirement")
	}
	if permission != ServerPermissionPower || !group {
		t.Fatalf("mcp:actions.request should map to group power access, got %q (group=%v)", permission, group)
	}

	// Holding it confers none of the direct execution scopes, and holding those
	// does not confer it.
	legacy := []string{string(MCPScopeActionsRequest)}
	for _, direct := range []MCPScope{MCPScopePowerStart, MCPScopePowerStop, MCPScopePowerRestart} {
		if HasMCPScope(legacy, direct) {
			t.Errorf("mcp:actions.request subsumed %s", direct)
		}
	}
	directSet := []string{
		string(MCPScopePowerStart), string(MCPScopePowerStop), string(MCPScopePowerRestart),
	}
	if HasMCPScope(directSet, MCPScopeActionsRequest) {
		t.Error("direct power scopes subsumed mcp:actions.request")
	}
}

// Normalization is the only door into the vocabulary, so anything shaped like a
// widening — a wildcard, a prefix, a near-miss — must be a rejection rather
// than a silent drop that leaves the caller believing it got less.
func TestScopeNormalizationRejectsWideningShapes(t *testing.T) {
	for _, scope := range []string{
		"*", "mcp:*", "mcp:power.*", "mcp:mods", "mcp:power",
		"mcp:backups.restore", "mcp:servers.delete", "mcp:console.write",
		// `mcp:files.read` stays rejected. Reading two fixed diagnostic files is
		// `mcp:logs.raw`; a scope that claimed file access would be a promise
		// this facade does not keep, and the day it is added is the day the
		// facade has a file capability.
		"mcp:files.read", "mcp:files.write", "mcp:nodes.read", "mcp:users.read",
		"MCP:POWER.START", "mcp:power.start.now", // case and stray suffixes never fold into a match
	} {
		if _, err := NormalizeMCPScopes([]string{"mcp:servers.read", scope}); !errors.Is(err, ErrMCPUnknownScope) {
			t.Errorf("%q should be rejected as unknown, got %v", scope, err)
		}
	}

	// Trailing/leading whitespace around a real scope is trimmed, not rejected.
	got, err := NormalizeMCPScopes([]string{"  mcp:power.start  "})
	if err != nil || len(got) != 1 || got[0] != string(MCPScopePowerStart) {
		t.Fatalf("a padded known scope should normalize, got %v / %v", got, err)
	}

	// The full advertised set round-trips, so the consent screen can offer
	// everything it lists.
	all, err := NormalizeMCPScopes(AllMCPScopes())
	if err != nil {
		t.Fatalf("the advertised vocabulary must normalize: %v", err)
	}
	if len(all) != len(AllMCPScopes()) {
		t.Fatalf("normalizing the advertised set lost scopes: %d of %d", len(all), len(AllMCPScopes()))
	}
}

// A scope set that carries no capability at all must be an error rather than an
// empty grant that silently authorizes nothing but still looks connected.
func TestEmptyScopeSetIsRefused(t *testing.T) {
	for _, in := range [][]string{nil, {}, {""}, {"   "}, {"", " "}} {
		if _, err := NormalizeMCPScopes(in); !errors.Is(err, ErrMCPNoScopes) {
			t.Errorf("%v should be ErrMCPNoScopes, got %v", in, err)
		}
	}
}

// Nothing in the vocabulary may name a capability this facade deliberately does
// not have. This is a naming guard: it catches a scope for files, restore, or
// node access being added before any tool implements it.
//
// "console" is deliberately absent from the list. It was forbidden while no
// console capability existed at all; `mcp:console.run` now implements a closed
// verb table, so the name is honest rather than a widening. "command" stays
// forbidden and is the guard that still matters here: a scope named for a
// command would mean the facade had started accepting one, which is exactly the
// property console.run preserves by never taking a command string.
//
// "file"/"files" stay forbidden for the same reason, and the raw-log scope is
// named `mcp:logs.raw` rather than `mcp:files.read` precisely to keep them so.
// Two constant paths are not a file capability, and a scope must not describe
// one the facade does not have — the name is the only part of this a human
// reads on the consent screen.
func TestVocabularyNamesNoForbiddenCapability(t *testing.T) {
	forbidden := []string{
		"command", "file", "files", "restore", "delete", "reinstall",
		"migrate", "node", "nodes", "user", "users", "admin", "secret", "token",
		"shell", "exec", "sql",
	}
	for _, scope := range AllMCPScopes() {
		lower := strings.ToLower(scope)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("scope %q names a forbidden capability %q", scope, bad)
			}
		}
	}
}
