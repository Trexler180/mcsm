package process

import (
	"os"
	"path/filepath"
	"strings"
)

// Geyser lets Bedrock Edition (mobile/console) players join a Java server; its
// auth companion Floodgate gives each Bedrock player a Java-side identity. This
// file detects those players (and the Geyser/Floodgate install) so the roster
// can flag them explicitly instead of treating them as ordinary Java accounts.

// defaultBedrockPrefix is Floodgate's out-of-the-box username prefix. Floodgate
// prepends it to every Bedrock player's gamertag on the Java side so their names
// can't collide with real Java accounts.
const defaultBedrockPrefix = "."

// defaultReplaceSpaces is Floodgate's out-of-the-box handling of gamertags
// containing spaces. Xbox gamertags may contain them and Java usernames may not,
// so Floodgate substitutes underscores.
const defaultReplaceSpaces = true

// floodgateCfg is the subset of Floodgate's config.yml that determines a Bedrock
// player's Java-side identity, and therefore what has to be written into
// whitelist.json for that player to be recognised.
type floodgateCfg struct {
	Prefix        string
	ReplaceSpaces bool
}

// defaultFloodgateCfg is what Floodgate does when no config can be read.
func defaultFloodgateCfg() floodgateCfg {
	return floodgateCfg{Prefix: defaultBedrockPrefix, ReplaceSpaces: defaultReplaceSpaces}
}

// isBedrockUUID reports whether a Java-side UUID was minted by Floodgate for a
// Bedrock player. Floodgate builds it as new UUID(0, xuid): the high 64 bits are
// all zero and the low 64 bits hold the player's non-zero Xbox XUID, giving the
// form 00000000-0000-0000-XXXX-XXXXXXXXXXXX. That all-zero prefix is the
// reliable, server-version-independent signal — a real Java UUID having 64
// leading zero bits is astronomically unlikely. The all-zero "nil" UUID is
// rejected (it has no XUID).
func isBedrockUUID(uuid string) bool {
	hex := strings.ReplaceAll(strings.ToLower(uuid), "-", "")
	if len(hex) != 32 {
		return false
	}
	lowNonZero := false
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false // not hex at all
		}
		if i < 16 {
			if c != '0' {
				return false // high bits not all zero -> ordinary Java UUID
			}
		} else if c != '0' {
			lowNonZero = true
		}
	}
	return lowNonZero
}

// readFloodgateConfig parses the identity-affecting keys out of a Floodgate
// config.yml. Keys absent from the file keep their Floodgate defaults.
// prefixFound reports specifically whether `username-prefix` was present (an
// explicit empty prefix is a real setting, so it yields ("", true)); read
// reports whether the file could be opened at all.
func readFloodgateConfig(path string) (cfg floodgateCfg, prefixFound, read bool) {
	cfg = defaultFloodgateCfg()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if v, ok := strings.CutPrefix(t, "username-prefix:"); ok {
			// Strip surrounding quotes: `"."`/`'.'` -> `.`, `""` -> ``.
			cfg.Prefix = strings.Trim(strings.TrimSpace(v), "\"'")
			prefixFound = true
			continue
		}
		if v, ok := strings.CutPrefix(t, "replace-spaces:"); ok {
			cfg.ReplaceSpaces = strings.EqualFold(strings.Trim(strings.TrimSpace(v), "\"'"), "true")
		}
	}
	return cfg, prefixFound, true
}

// floodgateConfig returns the effective Floodgate settings for a server and
// whether a username-prefix was read from an actual config file. Floodgate's
// config lives under config/floodgate (mod loaders) or plugins/floodgate
// (Spigot/Paper). A config that exists but sets no prefix still contributes its
// other values, so a server that only overrides replace-spaces is honoured.
func floodgateConfig(dir string) (floodgateCfg, bool) {
	fallback := defaultFloodgateCfg()
	for _, rel := range [][]string{
		{"config", "floodgate", "config.yml"},
		{"plugins", "floodgate", "config.yml"},
		{"plugins", "Geyser-Floodgate", "config.yml"},
	} {
		cfg, prefixFound, read := readFloodgateConfig(filepath.Join(dir, filepath.Join(rel...)))
		if !read {
			continue
		}
		if prefixFound {
			return cfg, true
		}
		fallback = cfg
	}
	return fallback, false
}

// bedrockPrefix returns the effective Floodgate username prefix for a server and
// whether it was read from an actual config file.
func bedrockPrefix(dir string) (prefix string, found bool) {
	cfg, found := floodgateConfig(dir)
	return cfg.Prefix, found
}

// GeyserInfo describes a server's Bedrock-bridge setup, surfaced to the UI so it
// can show that Bedrock players are supported even before any have joined.
//
// Geyser and Floodgate are reported separately because they mean different
// things for player identity: Geyser alone bridges the connection but leaves
// players authenticating with their own Java account, so they arrive with an
// ordinary Java UUID. Only Floodgate mints the Bedrock-specific identity that
// can be whitelisted ahead of a first join.
type GeyserInfo struct {
	Installed bool   `json:"installed"`
	Geyser    bool   `json:"geyser"`
	Floodgate bool   `json:"floodgate"`
	Prefix    string `json:"prefix,omitempty"`
}

// PlayersMeta is everything the players UI needs about a server that isn't about
// an individual player. WhitelistEnabled rides along because a whitelist entry
// is inert while the server's white-list property is off — a successful add that
// enforces nothing is the same class of silent no-op as a malformed entry.
type PlayersMeta struct {
	GeyserInfo
	WhitelistEnabled bool `json:"whitelist_enabled"`
}

// detectGeyser scans a server's plugins/ and mods/ directories for the Geyser
// and Floodgate jars (either can ship as a Spigot/Paper plugin or a Fabric/
// NeoForge mod) and reports the effective Bedrock username prefix.
func detectGeyser(dir string) GeyserInfo {
	info := GeyserInfo{}
	for _, sub := range []string{"plugins", "mods"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := strings.ToLower(e.Name())
			if strings.Contains(n, "geyser") {
				info.Geyser = true
			}
			if strings.Contains(n, "floodgate") {
				info.Floodgate = true
			}
		}
	}
	info.Installed = info.Geyser || info.Floodgate
	if info.Installed {
		prefix, _ := bedrockPrefix(dir)
		info.Prefix = prefix
	}
	return info
}

// stampBedrock flags a player as Bedrock when their identity matches Floodgate's
// signature: a Floodgate-minted UUID, or (when no UUID is known, e.g. an online
// player seen only via /list) the configured username prefix on their name. An
// empty prefix disables name-based detection to avoid flagging Java players.
func stampBedrock(p *Player, prefix string) {
	if p.Bedrock {
		return
	}
	if isBedrockUUID(p.UUID) {
		p.Bedrock = true
		return
	}
	if prefix != "" && strings.HasPrefix(p.Name, prefix) {
		p.Bedrock = true
	}
}

// hasUnsafeChar reports whether a string contains any whitespace or control
// character. Player names are written to a live server's stdin, so anything that
// could break out of a console command (newline, space, tab, control byte) must
// be rejected regardless of edition.
func hasUnsafeChar(s string) bool {
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

// hasControlChar is the weaker check for names that only ever reach a JSON
// config file, never stdin. A Floodgate name legitimately contains a space when
// the server sets replace-spaces: false, so rejecting spaces there would refuse
// valid players; control characters are still refused because they would corrupt
// the file the server parses.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

// validPlayerName reports whether a name is safe to act on. Plain Java names
// (validName) are always allowed. A Bedrock name is allowed when it is the
// server's Floodgate prefix followed by a Java-shaped core — this widens
// acceptance just enough for Floodgate-prefixed gamertags while still rejecting
// any whitespace/control character, preserving console-injection safety.
func validPlayerName(dir, name string) bool {
	if name == "" || hasUnsafeChar(name) {
		return false
	}
	if validName.MatchString(name) {
		return true
	}
	prefix, _ := bedrockPrefix(dir)
	if prefix == "" {
		return false
	}
	core, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	return validName.MatchString(core)
}
