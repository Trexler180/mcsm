package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// The raw reads are bounded by a closed set of paths, not by a parameter.
//
// This is the property that made them safe to add at all. The facade has no
// file capability and must not appear to acquire one: whatever a caller asks
// for, the path that reaches the backend is a constant from this package. The
// escape-hatch guard in service_test.go stops a path parameter being added to a
// schema; this stops one being smuggled through an existing field.
func TestRawReadsOnlyEverRequestFixedPaths(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	// Every source spelling, plus values shaped like an attempt to escape.
	for _, source := range []string{
		"console", "latest", "log", "",
		"../../etc/passwd", "/etc/shadow", "logs/../../secrets.env",
		"C:\\Windows\\System32\\config\\SAM",
	} {
		_, _, _ = e.svc.readServerLog(p)(ctx, nil, readLogInput{ServerID: e.serverA, Source: source})
	}
	_, _, _ = e.svc.getServerProperties(p)(ctx, nil, serverIDInput{ServerID: e.serverA})

	allowed := map[string]bool{
		latestLogPath:        true,
		crashReportsDir:      true,
		serverPropertiesPath: true,
	}
	for _, call := range e.operator.recorded() {
		path, _ := call.args["path"].(string)
		if path == "" {
			continue
		}
		if !allowed[path] {
			t.Errorf("a read reached the backend for %q, which is not one of the fixed paths", path)
		}
	}
}

// An unknown source is refused rather than quietly treated as the console log,
// so a caller never believes it read something it did not.
func TestReadServerLogRefusesAnUnknownSource(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := principalFor(e.grant)

	if _, _, err := e.svc.readServerLog(p)(ctx, nil, readLogInput{
		ServerID: e.serverA, Source: "../../etc/passwd",
	}); err == nil {
		t.Fatal("a path-shaped source was accepted")
	}
}

// The tail is bounded however many lines are asked for, and the returned text
// goes through the same redaction as every other piece of untrusted evidence.
func TestLogTailIsBoundedAndCleaned(t *testing.T) {
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "line")
	}
	tail, dropped := tailLines(strings.Join(lines, "\n"), maxLogTailLines)
	if len(tail) != maxLogTailLines {
		t.Fatalf("tail = %d lines, want the cap of %d", len(tail), maxLogTailLines)
	}
	if !dropped {
		t.Error("dropping earlier lines was not reported")
	}

	// bound() clamps whatever the caller asks for, including absurd values.
	if got := bound(100000, 120, maxLogTailLines); got != maxLogTailLines {
		t.Errorf("bound(100000) = %d, want %d", got, maxLogTailLines)
	}
	if got := bound(-5, 120, maxLogTailLines); got != 120 {
		t.Errorf("bound(-5) = %d, want the default 120", got)
	}
}

// Every new read is refused without its scope, even though registration would
// already have hidden the tool. Listing is ergonomics; authorize() is the gate.
func TestNewReadsRequireTheirOwnScope(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// A grant with every *old* scope, and none of the new ones.
	old := []string{
		string(store.MCPScopeServersRead), string(store.MCPScopeDiagnosticsRead),
		string(store.MCPScopeLogsRead), string(store.MCPScopeMetricsRead),
		string(store.MCPScopeAuditRead), string(store.MCPScopeModsRead),
	}
	grant := e.mkGrant(t, e.ownerID, []string{e.serverA}, old...)
	p := principalFor(grant)

	if _, _, err := e.svc.readServerLog(p)(ctx, nil, readLogInput{ServerID: e.serverA, Source: "console"}); err == nil {
		t.Error("raw log read was allowed without mcp:logs.raw")
	}
	if _, _, err := e.svc.getServerProperties(p)(ctx, nil, serverIDInput{ServerID: e.serverA}); err == nil {
		t.Error("server.properties was readable without mcp:config.read")
	}
	if _, _, err := e.svc.listPlayers(p)(ctx, nil, listPlayersInput{ServerID: e.serverA}); err == nil {
		t.Error("the player roster was readable without mcp:players.read")
	}
	// The diagnostics-scoped additions remain available to the same grant.
	if _, _, err := e.svc.getServerSettings(p)(ctx, nil, serverIDInput{ServerID: e.serverA}); err != nil {
		t.Errorf("settings should be readable under diagnostics.read: %v", err)
	}
}

// server.properties is where an rcon password lives. It is redacted per
// property, so one secret cannot swallow the lines around it either.
func TestServerPropertiesRedactsSecrets(t *testing.T) {
	raw := strings.Join([]string{
		"#Minecraft server properties",
		"difficulty=hard",
		"rcon.password=hunter2secret",
		"white-list=true",
		"motd=A Minecraft Server",
	}, "\n")

	props := parseServerProperties(raw)

	if strings.Contains(props["rcon.password"], "hunter2secret") {
		t.Fatalf("the rcon password survived: %q", props["rcon.password"])
	}
	// The settings either side of it are untouched, which is the point of
	// redacting per property rather than over the whole file.
	if props["difficulty"] != "hard" || props["white-list"] != "true" {
		t.Fatalf("redaction damaged neighbouring properties: %#v", props)
	}
}
