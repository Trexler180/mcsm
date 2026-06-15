package process

import (
	"path/filepath"
	"testing"
)

func TestIsBedrockUUID(t *testing.T) {
	cases := map[string]bool{
		// Floodgate: high 64 bits zero, low bits a non-zero XUID.
		"00000000-0000-0000-0009-01f5dc3c2a1b": true,
		"000000000000000000090 1f5dc3c2a1b":    false, // wrong length/space
		"00000000-0000-0000-0000-0000000a1b2c": true,  // tiny xuid, still non-zero
		// Ordinary Java UUIDs (random/offline) have non-zero high bits.
		"c62987a0-e1b0-3a37-a71b-31ced9d25995": false,
		"11111111-1111-1111-1111-111111111111": false,
		// The nil UUID is not a Bedrock player.
		"00000000-0000-0000-0000-000000000000": false,
		// Garbage.
		"":      false,
		"hello": false,
	}
	for uuid, want := range cases {
		if got := isBedrockUUID(uuid); got != want {
			t.Errorf("isBedrockUUID(%q) = %v, want %v", uuid, got, want)
		}
	}
	// Dashless form must work too.
	if !isBedrockUUID("00000000000000000009f1f5dc3c2a1b") {
		t.Error("isBedrockUUID should accept the dashless Floodgate form")
	}
}

func TestBedrockPrefix(t *testing.T) {
	// No config anywhere → default ".", found=false.
	empty := t.TempDir()
	if p, found := bedrockPrefix(empty); p != "." || found {
		t.Fatalf("bedrockPrefix(empty) = (%q,%v), want (\".\",false)", p, found)
	}

	// config/floodgate (mod-loader layout), quoted value.
	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "config", "floodgate", "config.yml"),
		"# Floodgate config\nusername-prefix: \"*\"\nreplace-spaces: true\n")
	if p, found := bedrockPrefix(mod); p != "*" || !found {
		t.Fatalf("bedrockPrefix(mod) = (%q,%v), want (\"*\",true)", p, found)
	}

	// plugins/floodgate (Spigot layout), explicit empty prefix.
	plug := t.TempDir()
	writeFile(t, filepath.Join(plug, "plugins", "floodgate", "config.yml"),
		"username-prefix: ''\n")
	if p, found := bedrockPrefix(plug); p != "" || !found {
		t.Fatalf("bedrockPrefix(plug) = (%q,%v), want (\"\",true)", p, found)
	}
}

func TestDetectGeyser(t *testing.T) {
	// Spigot/Paper: both jars under plugins/.
	spigot := t.TempDir()
	writeFile(t, filepath.Join(spigot, "plugins", "Geyser-Spigot.jar"), "jar")
	writeFile(t, filepath.Join(spigot, "plugins", "floodgate-spigot.jar"), "jar")
	writeFile(t, filepath.Join(spigot, "plugins", "floodgate", "config.yml"), "username-prefix: .\n")
	info := detectGeyser(spigot)
	if !info.Installed || !info.Geyser || !info.Floodgate || info.Prefix != "." {
		t.Fatalf("detectGeyser(spigot) = %#v", info)
	}

	// Fabric: Geyser-only mod under mods/, no Floodgate.
	fabric := t.TempDir()
	writeFile(t, filepath.Join(fabric, "mods", "Geyser-Fabric.jar"), "jar")
	info = detectGeyser(fabric)
	if !info.Installed || !info.Geyser || info.Floodgate {
		t.Fatalf("detectGeyser(fabric) = %#v", info)
	}
	if info.Prefix != "." { // default prefix surfaced even without a config
		t.Fatalf("detectGeyser(fabric) prefix = %q, want \".\"", info.Prefix)
	}

	// Vanilla Java server: nothing installed.
	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "plugins", "WorldEdit.jar"), "jar")
	if info := detectGeyser(plain); info.Installed || info.Geyser || info.Floodgate {
		t.Fatalf("detectGeyser(plain) = %#v, want all false", info)
	}
}

func TestStampBedrock(t *testing.T) {
	// By UUID, regardless of prefix.
	p := Player{Name: "Steve", UUID: "00000000-0000-0000-0009-01f5dc3c2a1b"}
	stampBedrock(&p, "")
	if !p.Bedrock {
		t.Error("Floodgate UUID should stamp Bedrock even with empty prefix")
	}

	// By prefix when no UUID is known (online player seen only via /list).
	p = Player{Name: ".Alex"}
	stampBedrock(&p, ".")
	if !p.Bedrock {
		t.Error("prefixed name should stamp Bedrock")
	}

	// A Java player must never be flagged.
	p = Player{Name: "Notch", UUID: "c62987a0-e1b0-3a37-a71b-31ced9d25995"}
	stampBedrock(&p, ".")
	if p.Bedrock {
		t.Error("Java player should not be stamped Bedrock")
	}

	// Empty prefix disables name-based detection.
	p = Player{Name: ".Alex"}
	stampBedrock(&p, "")
	if p.Bedrock {
		t.Error("empty prefix should not enable name-based detection")
	}
}

func TestValidPlayerName(t *testing.T) {
	// Floodgate configured with the default "." prefix.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "floodgate", "config.yml"), "username-prefix: .\n")

	valid := []string{"Steve", "A_player_99", ".BedrockGuy", ".Player_1"}
	for _, n := range valid {
		if !validPlayerName(dir, n) {
			t.Errorf("validPlayerName(%q) = false, want true", n)
		}
	}

	invalid := []string{
		"",                 // empty
		"bad name",         // space (injection risk)
		"evil\nop someone", // newline
		"toolongplayername_x", // >16 java chars
		".;rm",             // prefix + non-name core
		"@bad",             // unknown prefix char
	}
	for _, n := range invalid {
		if validPlayerName(dir, n) {
			t.Errorf("validPlayerName(%q) = true, want false", n)
		}
	}

	// With an explicitly empty prefix, only Java-form names are accepted.
	noPrefix := t.TempDir()
	writeFile(t, filepath.Join(noPrefix, "plugins", "floodgate", "config.yml"), "username-prefix: ''\n")
	if validPlayerName(noPrefix, ".Bedrock") {
		t.Error("empty prefix server should reject a dot-prefixed name")
	}
	if !validPlayerName(noPrefix, "JavaName") {
		t.Error("empty prefix server should still accept a Java name")
	}
}
