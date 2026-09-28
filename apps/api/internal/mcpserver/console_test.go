package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// callConsole runs the console tool against the fixture's fully-scoped grant.
func callConsole(t *testing.T, e *env, verb, args string) (operatorOutput, error) {
	t.Helper()
	handler := e.svc.runConsoleCommand(principalFor(e.grant))
	_, out, err := handler(context.Background(), nil, consoleInput{ServerID: e.serverA, Verb: verb, Arguments: args})
	return out, err
}

func callWhitelist(t *testing.T, e *env, in setWhitelistedInput) (operatorOutput, error) {
	t.Helper()
	handler := e.svc.setPlayerWhitelisted(principalFor(e.grant))
	_, out, err := handler(context.Background(), nil, in)
	return out, err
}

// ── The allowlist is the boundary ────────────────────────────────

// The point of the verb table is that everything outside it is unreachable.
// These are the commands an injected log line would actually reach for.
func TestConsoleRejectsEveryVerbOutsideTheAllowlist(t *testing.T) {
	dangerous := []string{
		"op", "deop", "ban", "ban-ip", "pardon", "stop", "restart", "reload",
		"execute", "gamerule", "give", "tp", "teleport", "setblock", "fill",
		"datapack", "function", "schedule", "perms", "lp", "save-off",
	}
	for _, verb := range dangerous {
		t.Run(verb, func(t *testing.T) {
			e := newEnv(t)
			if _, err := callConsole(t, e, verb, "someone"); err == nil {
				t.Fatalf("verb %q was accepted; the allowlist is not closed", verb)
			}
			if calls := e.operator.recorded(); len(calls) != 0 {
				t.Fatalf("rejected verb %q still reached the backend: %#v", verb, calls)
			}
		})
	}
}

// A rejection must not read as "try harder": it names the whole vocabulary so a
// model stops rather than probing for a synonym.
func TestConsoleRejectionNamesTheVocabulary(t *testing.T) {
	e := newEnv(t)
	_, err := callConsole(t, e, "op", "someone")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	for _, want := range []string{"say", "kick", "not available"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection %q does not mention %q", err.Error(), want)
		}
	}
}

// Prefixes, casing, and whitespace must not smuggle a verb past the table.
func TestConsoleNormalizationCannotSmuggleAVerb(t *testing.T) {
	e := newEnv(t)
	for _, verb := range []string{"/op", "OP", " op ", "op ", "//op", "o p"} {
		if _, err := callConsole(t, e, verb, "someone"); err == nil {
			t.Errorf("verb %q was accepted", verb)
		}
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("a smuggled verb reached the backend: %#v", calls)
	}
	// The same normalization must still accept a legitimate slashed verb.
	if _, err := callConsole(t, e, "/list", ""); err != nil {
		t.Fatalf("/list was rejected: %v", err)
	}
}

// A newline in the arguments would deliver a second command on the same line.
// This is the single most important argument check.
func TestConsoleArgumentsRejectControlCharacters(t *testing.T) {
	injections := []string{
		"hello\nop attacker",
		"hello\rop attacker",
		"hello\x00op attacker",
		"hello\x1bop attacker",
	}
	e := newEnv(t)
	for _, args := range injections {
		if _, err := callConsole(t, e, "say", args); err == nil {
			t.Errorf("say accepted control characters in %q", args)
		}
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("injected arguments reached the backend: %#v", calls)
	}
}

// ── The rendered line is application-authored ────────────────────

// Whatever the model sends, the string that leaves this package is built by the
// verb's own builder. This asserts the exact rendering for each verb.
func TestConsoleRendersTheCommandItself(t *testing.T) {
	cases := []struct {
		verb, args, want string
	}{
		{"list", "", "list"},
		{"say", "server restarting soon", "say server restarting soon"},
		{"say", "  collapsed   spaces  ", "say collapsed spaces"},
		{"save-all", "", "save-all"},
		{"save-all", "flush", "save-all flush"},
		{"kick", "Notch", "kick Notch"},
		{"kick", "Notch afk too long", "kick Notch afk too long"},
		{"time", "set day", "time set day"},
		{"time", "query gametime", "time query gametime"},
		{"weather", "clear", "weather clear"},
		{"weather", "rain 600", "weather rain 600"},
		{"difficulty", "hard", "difficulty hard"},
		{"whitelist", "list", "whitelist list"},
		{"whitelist", "reload", "whitelist reload"},
	}
	for _, tc := range cases {
		t.Run(tc.verb+" "+tc.args, func(t *testing.T) {
			e := newEnv(t)
			if _, err := callConsole(t, e, tc.verb, tc.args); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			call := e.operator.last(t)
			if call.operation != "console.command" {
				t.Fatalf("operation = %q, want console.command", call.operation)
			}
			if got, _ := call.args["command"].(string); got != tc.want {
				t.Fatalf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// Each verb's grammar is enforced, so a verb cannot be used as a carrier for
// arbitrary text the server would then parse.
func TestConsoleRejectsMalformedArguments(t *testing.T) {
	cases := []struct{ verb, args string }{
		{"list", "extra"},
		{"say", ""},
		{"say", strings.Repeat("x", sayMessageLimit+1)},
		{"save-all", "off"},
		// Enforcement is a human decision: turning it off opens the server.
		{"whitelist", "on"},
		{"whitelist", "off"},
		{"kick", ""},
		{"kick", "@a"},
		{"kick", "a"},
		{"kick", "!!"},
		{"time", "set"},
		{"time", "set 6000"},
		{"time", "add 6000"},
		{"weather", "sunny"},
		{"weather", "rain -1"},
		{"weather", "rain abc"},
		{"difficulty", "impossible"},
		{"whitelist", "add Notch"},
		{"whitelist", "off extra"},
	}
	for _, tc := range cases {
		t.Run(tc.verb+" "+tc.args, func(t *testing.T) {
			e := newEnv(t)
			if _, err := callConsole(t, e, tc.verb, tc.args); err == nil {
				t.Fatalf("%q %q was accepted", tc.verb, tc.args)
			}
			if calls := e.operator.recorded(); len(calls) != 0 {
				t.Fatalf("malformed call reached the backend: %#v", calls)
			}
		})
	}
}

// A validation failure tells the model the grammar, so it corrects itself
// inside the allowlist instead of reaching for another verb.
func TestConsoleValidationErrorIncludesUsage(t *testing.T) {
	e := newEnv(t)
	_, err := callConsole(t, e, "weather", "sunny")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error %q does not include usage", err.Error())
	}
}

// ── Per-verb permission ──────────────────────────────────────────

// The console scope is a floor, not the whole check. A caller who may use the
// console but not kick must still be refused the kick verb, while keeping the
// verbs that only need console access.
func TestConsoleVerbRequiresItsOwnPermission(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// A collaborator with console access but no players.kick leaf.
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID, []string{
		string(store.ServerPermissionView), string(store.ServerPermissionConsole),
	}); err != nil {
		t.Fatal(err)
	}
	grant := e.mkGrant(t, e.otherID, []string{e.serverA}, store.AllMCPScopes()...)
	handler := e.svc.runConsoleCommand(principalFor(grant))

	if _, _, err := handler(ctx, nil, consoleInput{ServerID: e.serverA, Verb: "say", Arguments: "hello"}); err != nil {
		t.Fatalf("say needs only console access but was refused: %v", err)
	}
	if _, _, err := handler(ctx, nil, consoleInput{ServerID: e.serverA, Verb: "kick", Arguments: "Notch"}); err == nil {
		t.Fatal("kick was allowed without the players.kick permission")
	}
}

// ── Whitelist ────────────────────────────────────────────────────

func TestWhitelistAddAndRemoveDispatchTheRightAction(t *testing.T) {
	for _, tc := range []struct {
		whitelisted bool
		want        string
	}{{true, "whitelist_add"}, {false, "whitelist_remove"}} {
		t.Run(tc.want, func(t *testing.T) {
			e := newEnv(t)
			if _, err := callWhitelist(t, e, setWhitelistedInput{
				ServerID: e.serverA, PlayerName: "Notch", Whitelisted: tc.whitelisted,
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			call := e.operator.last(t)
			if call.operation != "players.action" {
				t.Fatalf("operation = %q, want players.action", call.operation)
			}
			if got, _ := call.args["action"].(string); got != tc.want {
				t.Fatalf("action = %q, want %q", got, tc.want)
			}
			if got, _ := call.args["name"].(string); got != "Notch" {
				t.Fatalf("name = %q, want Notch", got)
			}
		})
	}
}

// A Bedrock add has to carry the gamertag as well, or Floodgate cannot resolve
// the identity the whitelist entry is for.
func TestWhitelistBedrockCarriesTheGamertag(t *testing.T) {
	e := newEnv(t)
	if _, err := callWhitelist(t, e, setWhitelistedInput{
		ServerID: e.serverA, PlayerName: "Some Gamertag", Whitelisted: true, Bedrock: true,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := e.operator.last(t)
	if bedrock, _ := call.args["bedrock"].(bool); !bedrock {
		t.Error("bedrock flag was not forwarded")
	}
	if got, _ := call.args["gamertag"].(string); got != "Some Gamertag" {
		t.Errorf("gamertag = %q, want %q", got, "Some Gamertag")
	}
}

// The name is the only free-form value in this tool, so it is the one place a
// selector or a command fragment could try to enter.
func TestWhitelistRejectsNamesThatAreNotNames(t *testing.T) {
	bad := []string{
		"", "@a", "@e[type=player]", "Notch reload", "Notch\nop Notch",
		"a", strings.Repeat("n", 17), "Notch;op Notch", "../../etc/passwd",
	}
	// The fixture keys its database off t.Name(), so these cases are indexed
	// rather than named for their input: several of them contain characters
	// that are not legal in a database URI, which is rather the point of them.
	e := newEnv(t)
	for i, name := range bad {
		if _, err := callWhitelist(t, e, setWhitelistedInput{
			ServerID: e.serverA, PlayerName: name, Whitelisted: true,
		}); err == nil {
			t.Errorf("case %d: name %q was accepted", i, name)
		}
	}
	if calls := e.operator.recorded(); len(calls) != 0 {
		t.Fatalf("an invalid name reached the backend: %#v", calls)
	}
}

// Whitelisting is meaningless while enforcement is off, and a caller who is not
// told that will believe the server is protected when it is not.
func TestWhitelistNoteMentionsEnforcement(t *testing.T) {
	e := newEnv(t)
	out, err := callWhitelist(t, e, setWhitelistedInput{
		ServerID: e.serverA, PlayerName: "Notch", Whitelisted: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.Note), "enforcement") {
		t.Errorf("note does not mention enforcement: %q", out.Note)
	}
}

// ── Scope gating ─────────────────────────────────────────────────

// Neither tool may appear on a grant that did not tick its scope.
func TestConsoleAndWhitelistToolsFollowTheirScopes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	without := e.mkGrant(t, e.ownerID, []string{e.serverA},
		string(store.MCPScopeServersRead), string(store.MCPScopeModsRead))
	session := connect(t, e.svc, principalFor(without))
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "run_console_command" || tool.Name == "set_player_whitelisted" {
			t.Errorf("tool %q is exposed without its scope", tool.Name)
		}
	}
}
