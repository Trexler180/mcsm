package process

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The log line a whitelist rejection produces is not standardised: it varies by
// Minecraft version, by fork, and by whether the server resolved the profile
// before refusing it. Each case here is a shape a real server emits.
func TestParseJoinDenialAcrossServerFlavours(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantName string
		wantUUID string
	}{
		{
			name:     "vanilla Disconnecting with a resolved profile",
			line:     `[12:34:56] [Server thread/INFO]: Disconnecting com.mojang.authlib.GameProfile@5f2c1e3d[id=069a79f4-44e9-4726-a5be-fca90e38aaf5,name=Notch,properties={},legacy=false] (/198.51.100.7:56789): You are not white-listed on this server!`,
			wantName: "Notch",
			wantUUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5",
		},
		{
			name:     "newer vanilla lost-connection phrasing",
			line:     `[12:34:56] [Server thread/INFO]: com.mojang.authlib.GameProfile@1b2c[id=069a79f4-44e9-4726-a5be-fca90e38aaf5,name=Notch,properties={},legacy=false] (/198.51.100.7:56789) lost connection: You are not white-listed on this server!`,
			wantName: "Notch",
			wantUUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5",
		},
		{
			// Paper logs the bare name and spells it without the hyphen.
			name:     "paper bare name",
			line:     `[12:34:56] [Server thread/INFO]: Disconnecting Steve (/198.51.100.7:56789): You are not whitelisted on this server!`,
			wantName: "Steve",
		},
		{
			// Offline mode refuses before the profile has an id.
			name:     "profile without a resolved id",
			line:     `[12:34:56] [Server thread/INFO]: Disconnecting com.mojang.authlib.GameProfile@7a[id=<null>,name=Steve,properties={},legacy=false] (/198.51.100.7:56789): You are not white-listed on this server!`,
			wantName: "Steve",
		},
		{
			// A Floodgate player arrives under a prefixed name and the
			// Floodgate-minted UUID; both have to survive parsing, because
			// whitelisting them needs the UUID specifically.
			name:     "bedrock player through floodgate",
			line:     `[12:34:56] [Server thread/INFO]: Disconnecting com.mojang.authlib.GameProfile@9c[id=00000000-0000-0000-0009-1eb1c1a4b6f2,name=.CoolGuy,properties={},legacy=false] (/198.51.100.7:56789): You are not white-listed on this server!`,
			wantName: ".CoolGuy",
			wantUUID: "00000000-0000-0000-0009-1eb1c1a4b6f2",
		},
		{
			name:     "operator replaced the kick message but kept the phrase",
			line:     `[12:34:56] [Server thread/INFO]: Disconnecting Steve (/198.51.100.7:56789): Sorry, you are not whitelisted — ask in Discord!`,
			wantName: "Steve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, uuid, reason, ok := parseJoinDenial(tc.line)
			if !ok {
				t.Fatalf("line was not recognised as a whitelist denial")
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if uuid != tc.wantUUID {
				t.Errorf("uuid = %q, want %q", uuid, tc.wantUUID)
			}
			if reason == "" {
				t.Error("reason should carry the server's own wording")
			}
		})
	}
}

// Everything this parser must stay silent about. A false positive here spams an
// operator with prompts to whitelist people who are not waiting — including, in
// the chat case, whoever a griefer decides to name.
func TestParseJoinDenialIgnoresEverythingElse(t *testing.T) {
	lines := []string{
		// A player typing the kick message into chat. The `<Name>` form means the
		// payload after `]: ` is not something the server said.
		`[12:34:56] [Server thread/INFO]: <Griefer> Disconnecting Victim (/1.2.3.4:1): You are not white-listed on this server!`,
		// The same attack aimed at the other phrasing, where the chat text does
		// reach the "<subject> lost connection: <reason>" shape.
		`[12:34:56] [Server thread/INFO]: <Griefer> Victim lost connection: You are not white-listed on this server!`,
		`[12:34:56] [Server thread/INFO]: <Griefer> lost connection: You are not white-listed on this server!`,
		// Other kick reasons have their own remedies; whitelisting is not one.
		`[12:34:56] [Server thread/INFO]: Disconnecting Steve (/198.51.100.7:56789): You are banned from this server!`,
		`[12:34:56] [Server thread/INFO]: Disconnecting Steve (/198.51.100.7:56789): The server is full!`,
		`[12:34:56] [Server thread/INFO]: Disconnecting Steve (/198.51.100.7:56789): Outdated client!`,
		// An ordinary quit.
		`[12:34:56] [Server thread/INFO]: Steve lost connection: Disconnected`,
		`[12:34:56] [Server thread/INFO]: Steve left the game`,
		// Operator console output about the whitelist, not a rejection.
		`[12:34:56] [Server thread/INFO]: Added Steve to the whitelist`,
		`[12:34:56] [Server thread/INFO]: Steve is not whitelisted`,
		// No console prefix at all — a stack trace line, or mod output.
		`Disconnecting Steve (/198.51.100.7:56789): You are not white-listed on this server!`,
	}
	for _, line := range lines {
		if name, _, _, ok := parseJoinDenial(line); ok {
			t.Errorf("false positive %q on: %s", name, line)
		}
	}
}

// A name is only useful if it can be acted on. Anything that could not be a
// username is dropped at the parser rather than carried into an alert and a
// whitelist write.
func TestParseJoinDenialRejectsImplausibleNames(t *testing.T) {
	lines := []string{
		// Longer than a username can be.
		`[12:34:56] [Server thread/INFO]: Disconnecting ThisNameIsFarTooLongToBeReal (/1.2.3.4:1): You are not white-listed on this server!`,
		// Empty subject.
		`[12:34:56] [Server thread/INFO]: Disconnecting : You are not white-listed on this server!`,
	}
	for _, line := range lines {
		if _, _, _, ok := parseJoinDenial(line); ok {
			t.Errorf("accepted an implausible name: %s", line)
		}
	}
}

// A refused client normally retries within seconds. Each retry must sharpen the
// existing record, not create a new alert.
func TestDenialLogCoalescesRetries(t *testing.T) {
	var d denialLog
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	d.record("Steve", "", "not white-listed", base)
	d.record("steve", "069a79f4-44e9-4726-a5be-fca90e38aaf5", "not white-listed", base.Add(3*time.Second))
	d.record("Alex", "", "not white-listed", base.Add(4*time.Second))

	if len(d.entries) != 2 {
		t.Fatalf("expected 2 records (one per player), got %d: %+v", len(d.entries), d.entries)
	}
	steve := d.entries[0]
	if steve.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", steve.Attempts)
	}
	// The retry carried an id the first attempt lacked; a whitelist write wants it.
	if steve.UUID != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Errorf("uuid = %q, want the id from the later attempt", steve.UUID)
	}
	if d.entries[1].Name != "Alex" {
		t.Errorf("second record = %q, want the other player", d.entries[1].Name)
	}

	// Past the coalescing window the same player is news again.
	d.record("Steve", "", "not white-listed", base.Add(denialCoalesce+time.Minute))
	if len(d.entries) != 3 {
		t.Fatalf("a retry after the window should start a new record, got %d", len(d.entries))
	}
}

func TestDenialLogMarksBedrockPlayers(t *testing.T) {
	var d denialLog
	d.record(".CoolGuy", "00000000-0000-0000-0009-1eb1c1a4b6f2", "nope", time.Now())
	if !d.entries[0].Bedrock {
		t.Error("a Floodgate UUID should mark the record as Bedrock")
	}
}

func TestDenialLogIsBounded(t *testing.T) {
	var d denialLog
	now := time.Now()
	for i := 0; i < denialCap*3; i++ {
		d.record("Player"+string(rune('A'+i%26))+string(rune('a'+i/26)), "", "nope", now)
	}
	if len(d.entries) > denialCap {
		t.Fatalf("entries = %d, want at most %d", len(d.entries), denialCap)
	}
}

// The record set means "still waiting on you". Letting the player in — by any
// route — has to clear it, or the panel keeps nagging about a solved problem.
func TestDenialLogPruneDropsResolvedAndStale(t *testing.T) {
	var d denialLog
	now := time.Now()
	d.record("Whitelisted", "", "nope", now)
	d.record("Waiting", "", "nope", now)
	d.record("Ancient", "", "nope", now.Add(-denialTTL-time.Minute))

	d.prune(func(name, uuid string) bool { return name == "Whitelisted" }, now)

	if len(d.entries) != 1 || d.entries[0].Name != "Waiting" {
		t.Fatalf("entries = %+v, want only the unresolved recent one", d.entries)
	}
}

// End to end through the Manager: a console line becomes a record on the status
// payload, and whitelisting the player clears it.
func TestManagerJoinDenialsResolveAgainstTheWhitelist(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(t.TempDir())
	m.RegisterDir("srv1", dir)

	m.recordJoinDenial("srv1", "Steve", "069a79f4-44e9-4726-a5be-fca90e38aaf5", "You are not white-listed on this server!")

	got := m.JoinDenials("srv1")
	if len(got) != 1 || got[0].Name != "Steve" {
		t.Fatalf("JoinDenials = %+v, want one record for Steve", got)
	}
	if info := m.Status("srv1"); len(info.JoinDenied) != 1 {
		t.Fatalf("status should carry the denial, got %+v", info.JoinDenied)
	}

	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Steve"}]`)

	if got := m.JoinDenials("srv1"); len(got) != 0 {
		t.Fatalf("whitelisting Steve should clear the record, got %+v", got)
	}
}

func TestConsumeLineReportsJoinDenials(t *testing.T) {
	inst := newInstance(t.TempDir(), "srv1", StartConfig{})
	var got []string
	inst.onJoinDenied = func(serverID, name, uuid, reason string) {
		got = append(got, strings.Join([]string{serverID, name, uuid}, "|"))
	}

	inst.consumeLine(`[12:34:56] [Server thread/INFO]: Disconnecting com.mojang.authlib.GameProfile@5f[id=069a79f4-44e9-4726-a5be-fca90e38aaf5,name=Notch,properties={},legacy=false] (/198.51.100.7:1): You are not white-listed on this server!`, "stdout")
	inst.consumeLine(`[12:34:56] [Server thread/INFO]: Notch joined the game`, "stdout")

	want := "srv1|Notch|069a79f4-44e9-4726-a5be-fca90e38aaf5"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("hook calls = %v, want exactly [%q]", got, want)
	}
}
