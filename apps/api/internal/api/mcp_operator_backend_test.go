package api

import (
	"context"
	"testing"

	"github.com/mcsm/api/internal/api/handlers"
	"github.com/mcsm/api/internal/mcpserver"
)

// The console operation carries a line that mcpserver already rendered from its
// verb table. This adapter must never be usable as a way to hand a command
// through on its own, so a missing or empty one is refused before any handler
// runs rather than forwarded as a blank command.
func TestConsoleOperationRequiresARenderedCommand(t *testing.T) {
	backend := &mcpOperatorBackend{
		mods:    &handlers.ModHandlers{},
		backups: &handlers.BackupHandlers{},
		players: &handlers.PlayersHandlers{},
		servers: &handlers.ServerHandlers{},
	}
	actor := mcpserver.OperatorActor{UserID: "u", GrantID: "g"}

	for _, args := range []map[string]any{
		nil,
		{},
		{"command": ""},
		{"command": 42},
		{"verb": "op"},
	} {
		if _, err := backend.Invoke(context.Background(), actor, "console.command", "srv", "", args); err == nil {
			t.Errorf("args %#v produced no error", args)
		}
	}
}

// An operation outside the closed vocabulary is refused rather than routed.
//
// The backend is fully populated on purpose. Half-constructing it makes every
// operation fail on the nil check instead, which would let this test keep
// passing while saying nothing — and did, once file operations were added and
// `files.read` moved from unsupported to supported-but-bounded. Its refusal is
// now about the path rather than the name, and lives in the allowlist test.
func TestUnsupportedOperatorOperationIsRefused(t *testing.T) {
	backend := &mcpOperatorBackend{
		mods:    &handlers.ModHandlers{},
		backups: &handlers.BackupHandlers{},
		players: &handlers.PlayersHandlers{},
		servers: &handlers.ServerHandlers{},
		files:   &handlers.FileHandlers{},
		tasks:   &handlers.TaskHandlers{},
		mc:      &handlers.MinecraftHandlers{},
	}
	for _, operation := range []string{
		"", "console", "players.op", "backups.restore", "files.write",
		"files.delete", "servers.create", "nodes.list", "mods.upload",
	} {
		if _, err := backend.Invoke(context.Background(), mcpserver.OperatorActor{}, operation, "srv", "", nil); err == nil {
			t.Errorf("operation %q was accepted", operation)
		}
	}
}

func TestOperatorResponseError(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "agent unavailable", raw: `{"error":"failed to register server directory"}`, want: "node agent unavailable"},
		{name: "safe not found", raw: `{"error":"mod not found"}`, want: "mod not found"},
		{name: "internal detail stays hidden", raw: `{"error":"upload failed: C:\\secret\\mods"}`, want: "operator operation failed"},
		{name: "invalid response", raw: `not json`, want: "operator operation failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := operatorResponseError([]byte(tt.raw)).Error(); got != tt.want {
				t.Fatalf("operatorResponseError() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A text response must survive as text.
//
// This is the bug that made the whole raw-log feature dead on arrival: the
// agent serves a log file as text/plain, the adapter insisted on JSON, and
// every read came back as "operation returned an invalid response". Nothing
// caught it because the facade's own tests stub this adapter out entirely, so
// the decode step had no coverage at all.
func TestOperatorTextResponsesAreNotForcedThroughJSON(t *testing.T) {
	logBody := "[20:32:16] [Server thread/INFO]: Starting minecraft server\n" +
		"[20:32:17] [Server thread/ERROR]: Could not load mod glitchcore\n"

	got, err := decodeOperatorBody("text/plain; charset=utf-8", []byte(logBody))
	if err != nil {
		t.Fatalf("a plain-text body was rejected: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("plain text decoded to %T, want a string", got)
	}
	if text != logBody {
		t.Errorf("the body was altered:\n got %q\nwant %q", text, logBody)
	}
}

// Structured handlers still decode, and a malformed JSON body from one is still
// an error rather than a string that happens to look like data.
func TestOperatorJSONResponsesStillDecode(t *testing.T) {
	got, err := decodeOperatorBody("application/json", []byte(`{"entries":[{"path":"crash.txt"}]}`))
	if err != nil {
		t.Fatalf("valid JSON was rejected: %v", err)
	}
	if _, ok := got.(map[string]any); !ok {
		t.Fatalf("JSON decoded to %T, want a map", got)
	}
	if _, err := decodeOperatorBody("application/json", []byte("{not json")); err == nil {
		t.Error("malformed JSON from a JSON handler was accepted")
	}
	// An empty body is a completed operation, not an error.
	if _, err := decodeOperatorBody("application/json", []byte("   ")); err != nil {
		t.Errorf("an empty body was rejected: %v", err)
	}
}

// The readable-file set is enforced here as well as in the facade, because a
// tool added later is the thing that would widen it and this is the last place
// that can still refuse.
func TestFileOperationsAreConfinedToTheAllowlist(t *testing.T) {
	backend := &mcpOperatorBackend{
		mods:    &handlers.ModHandlers{},
		backups: &handlers.BackupHandlers{},
		players: &handlers.PlayersHandlers{},
		servers: &handlers.ServerHandlers{},
		files:   &handlers.FileHandlers{},
		tasks:   &handlers.TaskHandlers{},
		mc:      &handlers.MinecraftHandlers{},
	}
	actor := mcpserver.OperatorActor{UserID: "u", GrantID: "g"}

	for _, path := range []string{
		"", "/etc/passwd", "/server.properties/../../secrets.env",
		"/logs/../.mcsm-secret-key", "/ops.json", "/whitelist.json",
		"/crash-reports/../logs/latest.log", `/crash-reports\..\ops.json`,
	} {
		for _, op := range []string{"files.read", "files.tree"} {
			if _, err := backend.Invoke(context.Background(), actor, op, "srv", "", map[string]any{"path": path}); err == nil {
				t.Errorf("%s accepted the path %q", op, path)
			}
		}
	}
}

// The paths the facade actually uses must pass that same check, or the guard
// above would be refusing the feature it is meant to bound.
func TestFacadeReadablePathsPassTheAllowlist(t *testing.T) {
	for _, path := range mcpserver.ReadablePaths() {
		if !mcpserver.IsReadablePath(path) {
			t.Errorf("the facade reads %q but the allowlist refuses it", path)
		}
	}
	// A crash report is addressed inside the crash-report directory.
	if !mcpserver.IsReadablePath("/crash-reports/crash-2026-08-17_20.32.16-server.txt") {
		t.Error("a crash report inside the crash-report directory was refused")
	}
}

// The delegated player handler behind `players.action` authorizes nothing —
// MCP already did, which is why it has no HTTP claims to re-derive a permission
// from. It therefore refuses nothing either: the action set it understands
// includes op, ban, ban_ip, and kick.
//
// The facade only ever sends the two whitelist actions, so those are the only
// ones this adapter forwards. Without the check, the first tool to pass an
// action value through would reach `op` with no gate anywhere on the path.
func TestPlayerActionsAreConfinedToTheWhitelistPair(t *testing.T) {
	backend := &mcpOperatorBackend{
		mods:    &handlers.ModHandlers{},
		backups: &handlers.BackupHandlers{},
		players: &handlers.PlayersHandlers{},
		servers: &handlers.ServerHandlers{},
		files:   &handlers.FileHandlers{},
		tasks:   &handlers.TaskHandlers{},
		mc:      &handlers.MinecraftHandlers{},
	}
	actor := mcpserver.OperatorActor{UserID: "u", GrantID: "g"}

	for _, action := range []any{
		"op", "deop", "ban", "pardon", "ban_ip", "pardon_ip", "kick",
		"", "whitelist_add ", "WHITELIST_ADD", nil, 42,
	} {
		args := map[string]any{"name": "Steve"}
		if action != nil {
			args["action"] = action
		}
		if _, err := backend.Invoke(context.Background(), actor, "players.action", "srv", "", args); err == nil {
			t.Errorf("players.action accepted the action %#v", action)
		}
	}
}

// ...and the two the facade does send must pass, or the guard above would be
// refusing the feature it exists to bound. They fail later, on the nil store
// behind the handler, which is past the point this test is about.
func TestWhitelistActionsPassTheDelegatedGuard(t *testing.T) {
	for _, action := range []string{"whitelist_add", "whitelist_remove"} {
		if !mcpserver.IsDelegatedPlayerAction(action) {
			t.Errorf("the facade issues %q but the adapter refuses it", action)
		}
	}
	for _, action := range []string{"op", "ban", "kick", "whitelist", ""} {
		if mcpserver.IsDelegatedPlayerAction(action) {
			t.Errorf("%q must not be a delegated player action", action)
		}
	}
}
