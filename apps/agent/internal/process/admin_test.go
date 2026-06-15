package process

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findPlayer(players []Player, name string) (Player, bool) {
	for _, p := range players {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Player{}, false
}

func TestOfflineUUID(t *testing.T) {
	// Reference value for "OfflinePlayer:TestUser01" computed the same way Java's
	// UUID.nameUUIDFromBytes does (MD5 + version 3 + IETF variant bits).
	const want = "c62987a0-e1b0-3a37-a71b-31ced9d25995"
	got := offlineUUID("TestUser01")
	if got != want {
		t.Fatalf("offlineUUID = %q, want %q", got, want)
	}
	if offlineUUID("TestUser01") != got {
		t.Fatal("offlineUUID is not deterministic")
	}
	// Version nibble must be 3, variant nibble one of 8/9/a/b.
	if got[14] != '3' {
		t.Fatalf("version nibble = %c, want 3", got[14])
	}
	if !strings.ContainsRune("89ab", rune(got[19])) {
		t.Fatalf("variant nibble = %c, want one of 8/9/a/b", got[19])
	}
}

func TestReadServerStateAndStamp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ops.json"), `[{"uuid":"u-op","name":"Alice","level":3}]`)
	writeFile(t, filepath.Join(dir, "whitelist.json"), `[{"uuid":"u-wl","name":"Alice"}]`)
	writeFile(t, filepath.Join(dir, "banned-players.json"), `[{"uuid":"u-ban","name":"Mallory","reason":"griefing"}]`)

	st := readServerState(dir)

	alice := Player{Name: "alice"} // lowercase to prove case-insensitive matching
	st.stamp(&alice)
	if !alice.Op || alice.OpLevel != 3 {
		t.Fatalf("Alice op=%v level=%d, want op level 3", alice.Op, alice.OpLevel)
	}
	if !alice.Whitelisted {
		t.Fatal("Alice should be whitelisted")
	}
	if alice.Banned {
		t.Fatal("Alice should not be banned")
	}
	if alice.UUID != "u-op" {
		t.Fatalf("Alice uuid = %q, want backfilled u-op", alice.UUID)
	}

	mallory := Player{Name: "Mallory"}
	st.stamp(&mallory)
	if !mallory.Banned || mallory.BanReason != "griefing" {
		t.Fatalf("Mallory banned=%v reason=%q", mallory.Banned, mallory.BanReason)
	}
	if mallory.UUID != "u-ban" {
		t.Fatalf("Mallory uuid = %q, want u-ban", mallory.UUID)
	}

	names := st.names()
	if _, ok := names["mallory"]; !ok {
		t.Fatal("names() should include the banned-only player mallory")
	}
}

func TestApplyOfflineActionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	opsPath := filepath.Join(dir, "ops.json")
	wlPath := filepath.Join(dir, "whitelist.json")
	banPath := filepath.Join(dir, "banned-players.json")

	// op — and idempotent re-op.
	if err := applyOfflineAction(dir, "op", "Steve", "uuid-steve", ""); err != nil {
		t.Fatal(err)
	}
	if err := applyOfflineAction(dir, "op", "Steve", "uuid-steve", ""); err != nil {
		t.Fatal(err)
	}
	ops := readOps(dir)
	if len(ops) != 1 || ops[0].Name != "Steve" || ops[0].Level != 4 || ops[0].UUID != "uuid-steve" {
		t.Fatalf("ops after double op = %#v", ops)
	}
	// The file must be valid, pretty-printed, newline-terminated JSON.
	raw, err := os.ReadFile(opsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("ops.json should end with a newline")
	}
	if !strings.Contains(string(raw), "\n  ") {
		t.Fatal("ops.json should be indented")
	}

	// whitelist add/remove.
	if err := applyOfflineAction(dir, "whitelist_add", "Steve", "uuid-steve", ""); err != nil {
		t.Fatal(err)
	}
	if wl := readWhitelist(dir); len(wl) != 1 || wl[0].Name != "Steve" {
		t.Fatalf("whitelist = %#v", wl)
	}
	if err := applyOfflineAction(dir, "whitelist_remove", "steve", "", ""); err != nil {
		t.Fatal(err)
	}
	if wl := readWhitelist(dir); len(wl) != 0 {
		t.Fatalf("whitelist after remove = %#v", wl)
	}

	// ban with a reason, then re-ban replaces the reason (no duplicate).
	if err := applyOfflineAction(dir, "ban", "Steve", "uuid-steve", "spam"); err != nil {
		t.Fatal(err)
	}
	if err := applyOfflineAction(dir, "ban", "Steve", "uuid-steve", "spam again"); err != nil {
		t.Fatal(err)
	}
	bans := readBannedPlayers(dir)
	if len(bans) != 1 || bans[0].Reason != "spam again" || bans[0].Expires != "forever" {
		t.Fatalf("bans = %#v", bans)
	}

	// pardon clears the ban.
	if err := applyOfflineAction(dir, "pardon", "STEVE", "", ""); err != nil {
		t.Fatal(err)
	}
	if bans := readBannedPlayers(dir); len(bans) != 0 {
		t.Fatalf("bans after pardon = %#v", bans)
	}

	// deop clears the op entry.
	if err := applyOfflineAction(dir, "deop", "steve", "", ""); err != nil {
		t.Fatal(err)
	}
	if ops := readOps(dir); len(ops) != 0 {
		t.Fatalf("ops after deop = %#v", ops)
	}

	// kick is online-only.
	if err := applyOfflineAction(dir, "kick", "Steve", "", ""); err == nil {
		t.Fatal("kick offline should return an error")
	}

	_ = wlPath
	_ = banPath
}

func TestParseItemsLegacyAndModern(t *testing.T) {
	legacy := map[string]any{
		"id":    "minecraft:diamond_sword",
		"Slot":  int64(0),
		"Count": int64(1),
		"tag": map[string]any{
			"Damage":  int64(150),
			"display": map[string]any{"Name": "Excalibur"},
			"Enchantments": []any{
				map[string]any{"id": "minecraft:unbreaking", "lvl": int64(3)},
				map[string]any{"id": "minecraft:sharpness", "lvl": int64(5)},
			},
		},
	}
	modern := map[string]any{
		"id":    "minecraft:diamond_pickaxe",
		"Slot":  int64(1),
		"count": int64(1),
		"components": map[string]any{
			"minecraft:damage":      int64(42),
			"minecraft:custom_name": `"Digger"`,
			"minecraft:enchantments": map[string]any{
				"levels": map[string]any{"minecraft:efficiency": int64(4)},
			},
		},
	}

	items := parseItems([]any{legacy, modern})
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	sword := items[0]
	if sword.Damage != 150 {
		t.Fatalf("sword damage = %d, want 150", sword.Damage)
	}
	if sword.CustomName != "Excalibur" {
		t.Fatalf("sword custom name = %q", sword.CustomName)
	}
	wantEnch := []Enchant{
		{ID: "minecraft:sharpness", Level: 5},
		{ID: "minecraft:unbreaking", Level: 3},
	}
	if !reflect.DeepEqual(sword.Enchantments, wantEnch) {
		t.Fatalf("sword enchants = %#v, want sorted %#v", sword.Enchantments, wantEnch)
	}

	pick := items[1]
	if pick.Damage != 42 {
		t.Fatalf("pick damage = %d, want 42", pick.Damage)
	}
	if pick.CustomName != "Digger" {
		t.Fatalf("pick custom name = %q, want Digger", pick.CustomName)
	}
	if len(pick.Enchantments) != 1 || pick.Enchantments[0] != (Enchant{ID: "minecraft:efficiency", Level: 4}) {
		t.Fatalf("pick enchants = %#v", pick.Enchantments)
	}
}

func TestPlainText(t *testing.T) {
	cases := map[string]string{
		`{"text":"Hello ","extra":[{"text":"World"}]}`: "Hello World",
		`"Quoted"`:  "Quoted",
		"PlainBare": "PlainBare",
	}
	for in, want := range cases {
		if got := plainText(in); got != want {
			t.Fatalf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadPlayerStats(t *testing.T) {
	dir := t.TempDir()
	uuid := "11111111-2222-3333-4444-555555555555"
	writeFile(t, filepath.Join(dir, "world", "stats", uuid+".json"), `{
	  "stats": {
	    "minecraft:custom": {
	      "minecraft:play_time": 24000,
	      "minecraft:deaths": 3,
	      "minecraft:player_kills": 1,
	      "minecraft:mob_kills": 50,
	      "minecraft:jump": 120,
	      "minecraft:walk_one_cm": 500000
	    }
	  }
	}`)

	s := readPlayerStats(dir, uuid)
	if s == nil {
		t.Fatal("expected stats, got nil")
	}
	want := PlayerStats{
		PlayTimeTicks: 24000,
		Deaths:        3,
		PlayerKills:   1,
		MobKills:      50,
		Jumps:         120,
		WalkedCm:      500000,
	}
	if *s != want {
		t.Fatalf("stats = %#v, want %#v", *s, want)
	}

	// Pre-1.17 fallback: play_one_minute carries the tick count.
	uuid2 := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeFile(t, filepath.Join(dir, "world", "stats", uuid2+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_one_minute":1200}}}`)
	if s2 := readPlayerStats(dir, uuid2); s2 == nil || s2.PlayTimeTicks != 1200 {
		t.Fatalf("fallback play time = %#v", s2)
	}

	// Missing file → nil, no error.
	if readPlayerStats(dir, "no-such-uuid") != nil {
		t.Fatal("missing stats file should yield nil")
	}
}

func TestOnlineMode(t *testing.T) {
	dir := t.TempDir()
	if !onlineMode(dir) {
		t.Fatal("missing server.properties should default to online-mode true")
	}
	writeFile(t, filepath.Join(dir, "server.properties"), "level-name=world\nonline-mode=false\n")
	if onlineMode(dir) {
		t.Fatal("online-mode=false should report false")
	}
	writeFile(t, filepath.Join(dir, "server.properties"), "online-mode=true\n")
	if !onlineMode(dir) {
		t.Fatal("online-mode=true should report true")
	}
}

func TestOnlineCommand(t *testing.T) {
	cases := []struct {
		action, name, reason, want string
	}{
		{"op", "Steve", "", "op Steve"},
		{"deop", "Steve", "", "deop Steve"},
		{"pardon", "Steve", "", "pardon Steve"},
		{"whitelist_add", "Steve", "", "whitelist add Steve"},
		{"whitelist_remove", "Steve", "", "whitelist remove Steve"},
		{"ban", "Steve", "", "ban Steve"},
		{"ban", "Steve", "rude", "ban Steve rude"},
		{"kick", "Steve", "afk", "kick Steve afk"},
	}
	for _, tc := range cases {
		got, err := onlineCommand(tc.action, tc.name, tc.reason)
		if err != nil {
			t.Fatalf("onlineCommand(%q) error: %v", tc.action, err)
		}
		if got != tc.want {
			t.Fatalf("onlineCommand(%q,%q,%q) = %q, want %q", tc.action, tc.name, tc.reason, got, tc.want)
		}
	}
	if _, err := onlineCommand("bogus", "Steve", ""); err == nil {
		t.Fatal("unknown action should error")
	}
}

func TestSanitizeReason(t *testing.T) {
	if got := sanitizeReason("  hello\nworld\r!  "); got != "hello world !" {
		t.Fatalf("sanitizeReason = %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := sanitizeReason(long); len(got) != 200 {
		t.Fatalf("sanitizeReason length = %d, want 200", len(got))
	}
}

func TestBuildOfflineRosterIncludesStateOnly(t *testing.T) {
	dir := t.TempDir()
	uuidA := "11111111-1111-1111-1111-111111111111"
	// A real playerdata file on disk (content irrelevant — OfflinePlayers only
	// stats the dir entries).
	writeFile(t, filepath.Join(dir, "world", "playerdata", uuidA+".dat"), "nbt")
	writeFile(t, filepath.Join(dir, "usercache.json"), `[{"name":"Alice","uuid":"`+uuidA+`"}]`)
	writeFile(t, filepath.Join(dir, "whitelist.json"), `[{"uuid":"`+uuidA+`","name":"Alice"}]`)
	writeFile(t, filepath.Join(dir, "banned-players.json"), `[{"uuid":"u-ghost","name":"Ghost","reason":"griefing"}]`)

	roster := buildOfflineRoster(dir, readServerState(dir))
	if len(roster) != 2 {
		t.Fatalf("roster size = %d, want 2 (%#v)", len(roster), roster)
	}

	alice, ok := findPlayer(roster, "Alice")
	if !ok || !alice.Whitelisted || alice.Online || alice.UUID != uuidA {
		t.Fatalf("Alice entry wrong: %#v", alice)
	}
	ghost, ok := findPlayer(roster, "Ghost")
	if !ok || !ghost.Banned || ghost.Online {
		t.Fatalf("Ghost (state-only ban) entry wrong: %#v", ghost)
	}
}
