package process

import "testing"

func TestConflictDetectorParsesFabricBlock(t *testing.T) {
	lines := []string{
		"[13:56:20] [main/INFO]: Loading Minecraft 26.1.2 with Fabric Loader 0.19.2",
		"[13:56:21] [main/WARN]: Mod resolution failed",
		"[13:56:21] [main/ERROR]: Incompatible mods found!",
		"net.fabricmc.loader.impl.FormattedException: Some of your mods are incompatible with the game or each other!",
		"A potential solution has been determined, this may resolve your problem:",
		"\t - Replace mod 'Async' (async) 0.2.2+alpha-26.1.2 with any version that is compatible with:",
		"\t\t - moonrise, any version",
		"\t - Replace mod 'Moonrise' (moonrise) 1.0.0+1234f5d with any version that is compatible with:",
		"\t\t - c2me 0.3.7+alpha.0.69+26.1.2",
		"\t - Replace mod 'Fast Noise' (zfastnoise) 1.0.32c+26.1.2 with any version that is compatible with:",
		"\t\t - moonrise, any version",
		"More details:",
		"\t - Mod 'Async' (async) 0.2.2+alpha-26.1.2 is incompatible with any version of mod 'Moonrise' (moonrise)...",
		"\tat net.fabricmc.loader.impl.FormattedException.ofLocalized(FormattedException.java:51)",
	}

	var d conflictDetector
	var mc *ModConflict
	for _, l := range lines {
		if got := d.feed(l); got != nil {
			mc = got
		}
	}

	if mc == nil {
		t.Fatal("expected a conflict, got nil")
	}
	if len(mc.Suggestions) != 3 {
		t.Fatalf("expected 3 suggestions, got %d: %+v", len(mc.Suggestions), mc.Suggestions)
	}

	want := []struct{ id, name, ver string }{
		{"async", "Async", "0.2.2+alpha-26.1.2"},
		{"moonrise", "Moonrise", "1.0.0+1234f5d"},
		{"zfastnoise", "Fast Noise", "1.0.32c+26.1.2"},
	}
	for i, w := range want {
		s := mc.Suggestions[i]
		if s.ModID != w.id || s.ModName != w.name || s.Version != w.ver {
			t.Errorf("suggestion %d = {%q %q %q}, want {%q %q %q}", i, s.ModName, s.ModID, s.Version, w.name, w.id, w.ver)
		}
		if s.Action != "replace" {
			t.Errorf("suggestion %d action = %q, want replace", i, s.Action)
		}
	}
	// The first suggestion's requirement bullet should be captured, and the
	// "More details:" bullet must NOT leak into requirements.
	if len(mc.Suggestions[0].Requirements) != 1 || mc.Suggestions[0].Requirements[0] != "moonrise, any version" {
		t.Errorf("async requirements = %+v, want [moonrise, any version]", mc.Suggestions[0].Requirements)
	}
	if got := mc.Suggestions[2].Requirements; len(got) != 1 {
		t.Errorf("zfastnoise requirements = %+v, want exactly 1 (no More details leak)", got)
	}
}
