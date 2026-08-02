package process

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// stubGeyserAPI points the resolver at a local server for the duration of a
// test and returns a counter of how many lookups actually reached it, which is
// how the caching tests tell a memoised call from a real one.
func stubGeyserAPI(t *testing.T, handler http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	prev := geyserAPIBase
	geyserAPIBase = srv.URL
	t.Cleanup(func() { geyserAPIBase = prev })
	return &calls
}

func TestFloodgateUUID(t *testing.T) {
	tests := []struct {
		name string
		xuid uint64
		want string
	}{
		{
			// The gamertag "Notch" as returned by GeyserMC's public API. Verified
			// against the live service rather than derived by hand.
			name: "real gamertag",
			xuid: 2535453759792258,
			want: "00000000-0000-0000-0009-01fb54b26482",
		},
		{
			// A small XUID must still zero-pad to full width, or the string is not
			// a parseable UUID.
			name: "small value pads",
			xuid: 1,
			want: "00000000-0000-0000-0000-000000000001",
		},
		{
			name: "all bits set",
			xuid: ^uint64(0),
			want: "00000000-0000-0000-ffff-ffffffffffff",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := floodgateUUID(tc.xuid)
			if got != tc.want {
				t.Fatalf("floodgateUUID(%d) = %q, want %q", tc.xuid, got, tc.want)
			}
			// Whatever we mint must be recognised as Bedrock when read back, or the
			// roster would show these players as ordinary Java accounts.
			if !isBedrockUUID(got) {
				t.Errorf("isBedrockUUID(%q) = false, want true", got)
			}
		})
	}
}

func TestFloodgateName(t *testing.T) {
	tests := []struct {
		name     string
		gamertag string
		cfg      floodgateCfg
		want     string
	}{
		{
			name:     "prefix applied",
			gamertag: "CoolGuy",
			cfg:      floodgateCfg{Prefix: ".", ReplaceSpaces: true},
			want:     ".CoolGuy",
		},
		{
			name:     "spaces become underscores",
			gamertag: "Cool Guy 42",
			cfg:      floodgateCfg{Prefix: ".", ReplaceSpaces: true},
			want:     ".Cool_Guy_42",
		},
		{
			// A server that disables the substitution really does end up with a
			// space in the Java-side name, so the entry has to carry one too.
			name:     "spaces kept when disabled",
			gamertag: "Cool Guy",
			cfg:      floodgateCfg{Prefix: ".", ReplaceSpaces: false},
			want:     ".Cool Guy",
		},
		{
			// Floodgate truncates to Minecraft's 16-character limit; an untruncated
			// entry would not match the name the player actually connects with.
			name:     "truncated to 16",
			gamertag: "AVeryLongGamertag",
			cfg:      floodgateCfg{Prefix: ".", ReplaceSpaces: true},
			want:     ".AVeryLongGamert",
		},
		{
			name:     "multi-character prefix eats into the limit",
			gamertag: "LongishGamertag",
			cfg:      floodgateCfg{Prefix: "BE_", ReplaceSpaces: true},
			want:     "BE_LongishGamert",
		},
		{
			name:     "empty prefix",
			gamertag: "CoolGuy",
			cfg:      floodgateCfg{Prefix: "", ReplaceSpaces: true},
			want:     "CoolGuy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := floodgateName(tc.gamertag, tc.cfg); got != tc.want {
				t.Fatalf("floodgateName(%q) = %q, want %q", tc.gamertag, got, tc.want)
			}
		})
	}
}

func TestGamertagFromInput(t *testing.T) {
	dotted := floodgateCfg{Prefix: ".", ReplaceSpaces: true}
	tests := []struct {
		name  string
		input string
		cfg   floodgateCfg
		want  string
	}{
		{"bare gamertag", "Cool Guy", dotted, "Cool Guy"},
		{"floodgate name", ".Cool_Guy", dotted, "Cool Guy"},
		{"floodgate name without spaces", ".CoolGuy", dotted, "CoolGuy"},
		{"surrounding whitespace", "  .Cool_Guy  ", dotted, "Cool Guy"},
		{
			// Underscores cannot appear in a real gamertag, so treating them as
			// spaces also rescues an admin who typed the name the wrong way round.
			name: "underscores typed by hand", input: "Cool_Guy", cfg: dotted, want: "Cool Guy",
		},
		{
			// With substitution off there is nothing to reverse, so an underscore
			// must be left alone.
			name: "no substitution leaves underscores",
			input: ".Cool_Guy", cfg: floodgateCfg{Prefix: ".", ReplaceSpaces: false},
			want: "Cool_Guy",
		},
		{
			// Nothing to strip when the server sets no prefix.
			name: "empty prefix", input: "CoolGuy",
			cfg:  floodgateCfg{Prefix: "", ReplaceSpaces: true},
			want: "CoolGuy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := gamertagFromInput(tc.input, tc.cfg); got != tc.want {
				t.Fatalf("gamertagFromInput(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidGamertag(t *testing.T) {
	valid := []string{"Notch", "Cool Guy", "Cool Guy 42", "a", "A1", "SixteenCharsXXXX"}
	for _, s := range valid {
		if !validGamertag(s) {
			t.Errorf("validGamertag(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                  // empty
		" Leading",          // leading space
		"Trailing ",         // trailing space
		"Double  Space",     // doubled space
		"Has_Underscore",    // not a gamertag character
		"Has.Dot",           // not a gamertag character
		"Seventeen CharsXX", // over the 16-character cap
		"New\nline",         // would corrupt the file it is written to
	}
	for _, s := range invalid {
		if validGamertag(s) {
			t.Errorf("validGamertag(%q) = true, want false", s)
		}
	}
}

func TestReadFloodgateConfigReplaceSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	writeFile(t, path, "username-prefix: \".\"\nreplace-spaces: false\n")
	cfg, prefixFound, read := readFloodgateConfig(path)
	if !read || !prefixFound {
		t.Fatalf("read=%v prefixFound=%v, want both true", read, prefixFound)
	}
	if cfg.ReplaceSpaces {
		t.Error("replace-spaces: false should disable substitution")
	}

	// Absent key keeps Floodgate's own default rather than Go's zero value.
	writeFile(t, path, "username-prefix: \".\"\n")
	cfg, _, _ = readFloodgateConfig(path)
	if cfg.ReplaceSpaces != defaultReplaceSpaces {
		t.Errorf("absent replace-spaces = %v, want the default %v", cfg.ReplaceSpaces, defaultReplaceSpaces)
	}

	// A config that sets only replace-spaces still applies, and reports that no
	// prefix was found so the search can continue elsewhere.
	writeFile(t, path, "replace-spaces: false\n")
	cfg, prefixFound, read = readFloodgateConfig(path)
	if !read || prefixFound {
		t.Fatalf("read=%v prefixFound=%v, want true/false", read, prefixFound)
	}
	if cfg.ReplaceSpaces {
		t.Error("replace-spaces should still be honoured without a prefix key")
	}

	// An unreadable file yields the defaults.
	cfg, _, read = readFloodgateConfig(filepath.Join(dir, "missing.yml"))
	if read {
		t.Error("missing file should report read=false")
	}
	if cfg.Prefix != defaultBedrockPrefix || cfg.ReplaceSpaces != defaultReplaceSpaces {
		t.Errorf("missing file cfg = %#v, want defaults", cfg)
	}
}

func TestWhitelistEnabled(t *testing.T) {
	// Vanilla defaults white-list to false, so an absent file or key must not
	// claim the whitelist is on.
	if whitelistEnabled(t.TempDir()) {
		t.Error("no server.properties should read as whitelist off")
	}

	on := t.TempDir()
	writeFile(t, filepath.Join(on, "server.properties"), "white-list=true\nonline-mode=true\n")
	if !whitelistEnabled(on) {
		t.Error("white-list=true should read as on")
	}

	off := t.TempDir()
	writeFile(t, filepath.Join(off, "server.properties"), "white-list=false\n")
	if whitelistEnabled(off) {
		t.Error("white-list=false should read as off")
	}

	// enforce-whitelist is a different setting and must not be mistaken for it.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "server.properties"), "enforce-whitelist=true\n")
	if whitelistEnabled(other) {
		t.Error("enforce-whitelist should not satisfy white-list")
	}
}

// floodgateServer builds a temp server directory with Floodgate installed.
func floodgateServer(t *testing.T, configBody string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "floodgate-spigot.jar"), "jar")
	writeFile(t, filepath.Join(dir, "plugins", "floodgate", "config.yml"), configBody)
	return dir
}

func TestResolveBedrock(t *testing.T) {
	stubGeyserAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/xbox/xuid/Cool Guy" {
			w.Write([]byte(`{"xuid":2535453759792258}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	m := NewManager(t.TempDir())
	const id = "srv1"
	m.RegisterDir(id, floodgateServer(t, "username-prefix: .\n"))

	ident, err := m.ResolveBedrock(id, "Cool Guy")
	if err != nil {
		t.Fatalf("ResolveBedrock: %v", err)
	}
	if ident.UUID != "00000000-0000-0000-0009-01fb54b26482" {
		t.Errorf("uuid = %q", ident.UUID)
	}
	if ident.Name != ".Cool_Guy" {
		t.Errorf("name = %q, want .Cool_Guy", ident.Name)
	}
	if ident.XUID != "2535453759792258" {
		t.Errorf("xuid = %q", ident.XUID)
	}
	if ident.Gamertag != "Cool Guy" {
		t.Errorf("gamertag = %q", ident.Gamertag)
	}

	// The prefixed in-game name resolves to the same identity, so an admin can
	// paste either form.
	viaName, err := m.ResolveBedrock(id, ".Cool_Guy")
	if err != nil {
		t.Fatalf("ResolveBedrock via floodgate name: %v", err)
	}
	if viaName != ident {
		t.Errorf("prefixed name resolved differently: %#v vs %#v", viaName, ident)
	}

	// An unknown gamertag is reported as a bad gamertag, not an outage.
	if _, err := m.ResolveBedrock(id, "Nobody"); !errors.Is(err, ErrGamertagNotFound) {
		t.Errorf("unknown gamertag err = %v, want ErrGamertagNotFound", err)
	}

	// Malformed input never reaches the network.
	if _, err := m.ResolveBedrock(id, "bad_name!"); err == nil {
		t.Error("malformed gamertag should be rejected")
	}
}

func TestResolveBedrockDistinguishesOutage(t *testing.T) {
	stubGeyserAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	m := NewManager(t.TempDir())
	m.RegisterDir("srv1", floodgateServer(t, "username-prefix: .\n"))

	// Being rate-limited says nothing about the gamertag, so it must not be
	// reported as "no such player" — the admin would give up on a valid name.
	_, err := m.ResolveBedrock("srv1", "Cool Guy")
	if !errors.Is(err, ErrBedrockLookupUnavailable) {
		t.Fatalf("err = %v, want ErrBedrockLookupUnavailable", err)
	}
}

func TestResolveBedrockEmptyXUIDIsNotFound(t *testing.T) {
	// The service answers 200 with a message body for some unknown gamertags.
	stubGeyserAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"message":"Unable to find user"}`))
	})
	m := NewManager(t.TempDir())
	m.RegisterDir("srv1", floodgateServer(t, "username-prefix: .\n"))

	if _, err := m.ResolveBedrock("srv1", "Nobody"); !errors.Is(err, ErrGamertagNotFound) {
		t.Fatalf("err = %v, want ErrGamertagNotFound", err)
	}
}

func TestLookupXUIDCaching(t *testing.T) {
	calls := stubGeyserAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/xbox/xuid/Known" {
			w.Write([]byte(`{"xuid":42}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	m := NewManager(t.TempDir())

	for i := 0; i < 3; i++ {
		if _, err := m.lookupXUID("Known"); err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("hit made %d calls, want 1 (the rest memoised)", got)
	}

	// Case differences must share a cache entry; gamertags are case-insensitive.
	if _, err := m.lookupXUID("known"); err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("case variant made %d calls, want 1", got)
	}

	// Misses are cached too, so backspacing through a typo doesn't re-query.
	for i := 0; i < 3; i++ {
		if _, err := m.lookupXUID("Nobody"); !errors.Is(err, ErrGamertagNotFound) {
			t.Fatalf("miss %d: err = %v", i, err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("miss made %d total calls, want 2", got)
	}
}

func TestLookupXUIDDoesNotCacheOutages(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	calls := stubGeyserAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"xuid":42}`))
	})
	m := NewManager(t.TempDir())

	if _, err := m.lookupXUID("Known"); !errors.Is(err, ErrBedrockLookupUnavailable) {
		t.Fatalf("first lookup err = %v", err)
	}
	// A transient failure is not a fact about the gamertag: the retry must reach
	// the network rather than replay a ten-minute-old outage.
	fail.Store(false)
	xuid, err := m.lookupXUID("Known")
	if err != nil {
		t.Fatalf("retry after outage: %v", err)
	}
	if xuid != 42 {
		t.Errorf("xuid = %d, want 42", xuid)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d calls, want 2 (outage not cached)", got)
	}
}
