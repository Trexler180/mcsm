package mcpserver

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/store"
)

// This file adds the only two player-facing write paths in the facade: a
// whitelist toggle, and a *closed* console vocabulary.
//
// The console tool is not a console proxy, and the difference is the whole
// security argument. A model never supplies a command string. It supplies a
// verb from the table below plus arguments that the matching builder parses,
// and the builder — application code, not model output — renders the line that
// reaches the server. A verb outside the table cannot be expressed, so `op`,
// `ban`, `stop`, `execute`, `reload`, and every mod-added command remain
// unreachable however the model is steered, including by the untrusted log and
// mod text this same facade hands back as evidence.
//
// Adding a verb is therefore a deliberate security decision, not a config
// tweak. Before adding one, ask whether it can grant authority (op, permission
// plugins), destroy state irreversibly (ban, kick-with-ban side effects, world
// edits), affect availability (stop, restart, reload), or take a nested command
// as an argument (execute, schedule) — any yes belongs outside this table.

var (
	// Java names are the documented 3-16 word characters. Bedrock gamertags are
	// wider (spaces and a few punctuation marks), so they get their own pattern
	// rather than loosening the Java one.
	javaPlayerName    = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)
	bedrockPlayerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{2,31}$`)
	consoleVerbName   = regexp.MustCompile(`^[a-z-]{1,16}$`)
)

// consoleArgsLimit bounds the argument string before it is even parsed, so a
// pathological input is rejected on length rather than by a builder.
const consoleArgsLimit = 256

// sayMessageLimit keeps a broadcast to something a chat line can hold.
const sayMessageLimit = 200

type consoleVerb struct {
	// permission is the leaf a caller must hold *in addition to* the console
	// permission the scope already consented to. It is the verb's real-world
	// effect expressed as RBAC: kicking needs players.kick even though the
	// caller reached this tool through the console scope.
	permission store.ServerPermission
	group      bool
	// usage is shown to the model on a validation failure so it can correct
	// itself without guessing at the grammar.
	usage string
	// build parses arguments and renders the exact console line. Returning an
	// error rejects the call; there is no partial or best-effort rendering.
	build func(args string) (string, error)
}

// consoleVerbs is the closed vocabulary. Every entry is reversible, affects
// neither authority nor availability, and takes no nested command.
var consoleVerbs = map[string]consoleVerb{
	"list": {
		permission: store.ServerPermissionConsole,
		usage:      "list (no arguments)",
		build: func(args string) (string, error) {
			if args != "" {
				return "", toolError("list takes no arguments")
			}
			return "list", nil
		},
	},
	"say": {
		permission: store.ServerPermissionConsole,
		usage:      "say <message>",
		build: func(args string) (string, error) {
			if args == "" || len(args) > sayMessageLimit {
				return "", toolError("say needs a message of 1-%d characters", sayMessageLimit)
			}
			return "say " + args, nil
		},
	},
	"save-all": {
		permission: store.ServerPermissionConsole,
		usage:      "save-all [flush]",
		build: func(args string) (string, error) {
			switch args {
			case "":
				return "save-all", nil
			case "flush":
				return "save-all flush", nil
			}
			return "", toolError("save-all takes no arguments or the single word flush")
		},
	},
	"kick": {
		permission: store.ServerPermissionPlayersKick,
		usage:      "kick <player> [reason]",
		build: func(args string) (string, error) {
			name, rest, _ := strings.Cut(args, " ")
			if !javaPlayerName.MatchString(name) {
				return "", toolError("kick needs a valid player name first")
			}
			reason := strings.TrimSpace(rest)
			if reason == "" {
				return "kick " + name, nil
			}
			if len(reason) > sayMessageLimit {
				return "", toolError("kick reason must be at most %d characters", sayMessageLimit)
			}
			return "kick " + name + " " + reason, nil
		},
	},
	"time": {
		permission: store.ServerPermissionConsole,
		usage:      "time set day|night|noon|midnight, or time query day|daytime|gametime",
		build: func(args string) (string, error) {
			mode, value, ok := strings.Cut(args, " ")
			if !ok {
				return "", toolError("time needs a set or query argument")
			}
			value = strings.TrimSpace(value)
			switch mode {
			case "set":
				if !oneOf(value, "day", "night", "noon", "midnight") {
					return "", toolError("time set accepts day, night, noon, or midnight")
				}
			case "query":
				if !oneOf(value, "day", "daytime", "gametime") {
					return "", toolError("time query accepts day, daytime, or gametime")
				}
			default:
				return "", toolError("time accepts set or query")
			}
			return "time " + mode + " " + value, nil
		},
	},
	"weather": {
		permission: store.ServerPermissionConsole,
		usage:      "weather clear|rain|thunder [duration-seconds]",
		build: func(args string) (string, error) {
			kind, duration, _ := strings.Cut(args, " ")
			if !oneOf(kind, "clear", "rain", "thunder") {
				return "", toolError("weather accepts clear, rain, or thunder")
			}
			duration = strings.TrimSpace(duration)
			if duration == "" {
				return "weather " + kind, nil
			}
			seconds, err := strconv.Atoi(duration)
			if err != nil || seconds < 1 || seconds > 1_000_000 {
				return "", toolError("weather duration must be a whole number of seconds, 1-1000000")
			}
			return "weather " + kind + " " + strconv.Itoa(seconds), nil
		},
	},
	"difficulty": {
		permission: store.ServerPermissionConsole,
		usage:      "difficulty peaceful|easy|normal|hard",
		build: func(args string) (string, error) {
			if !oneOf(args, "peaceful", "easy", "normal", "hard") {
				return "", toolError("difficulty accepts peaceful, easy, normal, or hard")
			}
			return "difficulty " + args, nil
		},
	},
	// whitelist here covers only listing entries and re-reading the file.
	// Adding and removing a player goes through set_player_whitelisted, which
	// works whether or not the server is running. Switching enforcement on or
	// off is deliberately absent: turning it off opens the server to anyone who
	// can reach it, which is an availability and access decision rather than a
	// reversible convenience, so it stays with a human in the dashboard.
	"whitelist": {
		permission: store.ServerPermissionPlayersWhitelist,
		usage:      "whitelist list|reload",
		build: func(args string) (string, error) {
			if !oneOf(args, "list", "reload") {
				return "", toolError("whitelist accepts list or reload; use set_player_whitelisted to add or remove a player, and turning enforcement on or off is only available in the dashboard")
			}
			return "whitelist " + args, nil
		},
	},
}

// delegatedPlayerActions is every player write this facade issues, named the
// way the agent's own action set names them.
//
// The agent understands `op`, `ban`, `ban_ip`, and `kick` on the same endpoint,
// and the delegated handler behind it authorizes nothing by itself — MCP
// already did that, which is precisely why it has no claims to re-derive a
// permission from. So this set is the last thing standing between the facade
// and those verbs, and it exists for the same reason IsReadablePath does: the
// tool that would widen it is the one nobody has written yet.
var delegatedPlayerActions = map[string]bool{
	"whitelist_add":    true,
	"whitelist_remove": true,
}

// IsDelegatedPlayerAction reports whether a player action is one the facade
// actually issues. The operator adapter checks it before dispatching, so a
// future caller that passes an action through cannot reach `op` or `ban`.
func IsDelegatedPlayerAction(action string) bool { return delegatedPlayerActions[action] }

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// consoleVerbList is the vocabulary rendered for tool descriptions and errors,
// in a stable order so the tool schema does not churn between builds.
var consoleVerbList = []string{"list", "say", "save-all", "kick", "time", "weather", "difficulty", "whitelist"}

type setWhitelistedInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
	// PlayerName, not "player": the value is a name to write, and naming the
	// field for the entity would invite a uuid, a selector, or an @a target.
	PlayerName  string `json:"player_name" jsonschema:"exact player name; a Java username, or an Xbox gamertag when bedrock is true"`
	Whitelisted bool   `json:"whitelisted" jsonschema:"true to add the player to the whitelist, false to remove them"`
	Bedrock     bool   `json:"bedrock,omitempty" jsonschema:"true when the name is an Xbox gamertag for a Bedrock player joining through Geyser/Floodgate"`
}

type consoleInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
	// Verb and Arguments are deliberately separate. One free-form string would
	// be a command, and this facade does not accept commands.
	Verb      string `json:"verb" jsonschema:"one allowlisted console verb: list, say, save-all, kick, time, weather, difficulty, or whitelist"`
	Arguments string `json:"arguments,omitempty" jsonschema:"arguments for that verb only, in the grammar the verb documents; never another command"`
}

func (svc *Service) registerConsoleTools(s *mcp.Server, p *Principal) {
	if p.HasScope(store.MCPScopePlayersWhitelist) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{
			Name:  "set_player_whitelisted",
			Title: "Whitelist or un-whitelist a player",
			Description: "Add one named player to a server's whitelist, or remove them. Works whether the server is running or stopped. " +
				"It cannot op, ban, or kick anyone, and it does not turn whitelist enforcement itself on or off.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: true},
		}, svc.setPlayerWhitelisted(p))
	}
	if p.HasScope(store.MCPScopeConsoleRun) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{
			Name:  "run_console_command",
			Title: "Run an allowlisted console command",
			Description: "Run one console command from a fixed allowlist: " + strings.Join(consoleVerbList, ", ") + ". " +
				"You choose a verb and its arguments; the server renders the command. Anything outside this list — op, ban, stop, execute, reload, gamerule, and every mod-added command — cannot be expressed by this tool and is not available through this server. " +
				"The server must be running.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false},
		}, svc.runConsoleCommand(p))
	}
}

func (svc *Service) setPlayerWhitelisted(p *Principal) mcp.ToolHandlerFor[setWhitelistedInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in setWhitelistedInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopePlayersWhitelist, store.ServerPermissionPlayersWhitelist, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		name := strings.TrimSpace(in.PlayerName)
		if err := validPlayerName(name, in.Bedrock); err != nil {
			return nil, operatorOutput{}, err
		}

		action := "whitelist_remove"
		if in.Whitelisted {
			action = "whitelist_add"
		}
		args := map[string]any{"action": action, "name": name, "bedrock": in.Bedrock}
		if in.Bedrock {
			// A Bedrock add resolves the gamertag to a Floodgate identity, so the
			// name travels in both fields exactly as the dashboard sends it.
			args["gamertag"] = name
		}
		result, err := svc.invokeOperator(ctx, p, "players.action", srv.ID, "", args)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		svc.auditOperator(ctx, p, srv.ID, "players."+action, map[string]any{"bedrock": in.Bedrock})

		note := "The whitelist file was updated. Whitelist enforcement itself is a separate setting; a change here does nothing while enforcement is off."
		if in.Whitelisted {
			note += " Adding a player does not grant them any authority."
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: note}, nil
	}
}

func (svc *Service) runConsoleCommand(p *Principal) mcp.ToolHandlerFor[consoleInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in consoleInput) (*mcp.CallToolResult, operatorOutput, error) {
		// The console scope is the floor. It is checked first so a caller
		// without it learns nothing about which verbs exist.
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeConsoleRun, store.ServerPermissionConsole, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}

		verbName := strings.ToLower(strings.TrimSpace(in.Verb))
		// Trimming a leading slash is a convenience, not a parse: the remainder
		// still has to match a table entry exactly.
		verbName = strings.TrimPrefix(verbName, "/")
		if !consoleVerbName.MatchString(verbName) {
			return nil, operatorOutput{}, unsupportedVerbError()
		}
		verb, ok := consoleVerbs[verbName]
		if !ok {
			return nil, operatorOutput{}, unsupportedVerbError()
		}

		// The verb's own permission is a second, narrower gate on top of the
		// console permission already established above.
		if _, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeConsoleRun, verb.permission, verb.group); err != nil {
			return nil, operatorOutput{}, err
		}

		args, err := normalizeConsoleArgs(in.Arguments)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		line, err := verb.build(args)
		if err != nil {
			return nil, operatorOutput{}, toolError("%s (usage: %s)", err.Error(), verb.usage)
		}

		result, err := svc.invokeOperator(ctx, p, "console.command", srv.ID, "", map[string]any{"command": line})
		if err != nil {
			// A stopped server is by far the most common cause, and the backend
			// error is deliberately generic. Offer that as a lead to check, not
			// as a diagnosis — the call may have failed for another reason.
			return nil, operatorOutput{}, toolError("%s; a console command needs the server to be running, which get_server_diagnostics will confirm", err.Error())
		}
		svc.auditOperator(ctx, p, srv.ID, "console.command", map[string]any{"verb": verbName, "rendered": clean(line, 240)})

		return nil, operatorOutput{
			Server: serverRef(srv),
			Result: result,
			Note: "The command was sent to the running server. This returns only whether it was delivered, not its output; " +
				"read the effect with get_recent_log_events or get_server_diagnostics.",
		}, nil
	}
}

// unsupportedVerbError names the whole vocabulary rather than only rejecting,
// so a model that guessed wrong retries inside the allowlist instead of probing
// for what else might be accepted.
func unsupportedVerbError() error {
	return toolError("unsupported console verb; this server accepts only: %s. Anything else, including op, ban, stop, and execute, is not available through this server", strings.Join(consoleVerbList, ", "))
}

// normalizeConsoleArgs collapses whitespace and rejects anything that is not
// plain printable text. Control characters are the specific concern: a newline
// would let one call deliver a second command to the console.
func normalizeConsoleArgs(raw string) (string, error) {
	if len(raw) > consoleArgsLimit {
		return "", toolError("arguments must be at most %d characters", consoleArgsLimit)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", toolError("arguments must not contain control characters or line breaks")
		}
	}
	return strings.Join(strings.Fields(raw), " "), nil
}

func validPlayerName(name string, bedrock bool) error {
	if bedrock {
		if !bedrockPlayerName.MatchString(name) {
			return toolError("gamertag must be 3-32 characters of letters, digits, spaces, dots, underscores, or hyphens")
		}
		return nil
	}
	if !javaPlayerName.MatchString(name) {
		return toolError("player name must be 3-16 letters, digits, or underscores")
	}
	return nil
}
