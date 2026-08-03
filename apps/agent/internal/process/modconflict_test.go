package process

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// The overwhelmingly common modded-server failure: mods are installed but
// Fabric API isn't. The loader names it by bare mod id in the solution block
// ("Install fabric, any version.") — a shape that carries no `mod '<name>'
// (<id>)` and so parsed to zero suggestions before missing-dependency support.
func TestConflictDetectorParsesMissingFabricAPI(t *testing.T) {
	lines := []string{
		"[13:56:20] [main/INFO]: Loading Minecraft 1.21.1 with Fabric Loader 0.16.5",
		"[13:56:21] [main/ERROR]: Incompatible mods found!",
		"net.fabricmc.loader.impl.FormattedException: Some of your mods are incompatible with the game or each other!",
		"A potential solution has been determined, this may resolve your problem:",
		"\t - Install fabric, any version.",
		"More details:",
		"\t - Mod 'AppleSkin' (appleskin) 2.5.1+mc1.21 requires any version of fabric, which is missing!",
		"\t - Mod 'Sodium' (sodium) 0.6.0 requires version 0.100.0 or later of fabric, which is missing!",
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
	if mc.Kind != "missing_dependency" {
		t.Errorf("kind = %q, want missing_dependency", mc.Kind)
	}
	// The dependency is named three times (once as a solution, twice as a
	// requirement) but must collapse to a single installable suggestion.
	if len(mc.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %+v", len(mc.Suggestions), mc.Suggestions)
	}
	s := mc.Suggestions[0]
	if s.Action != "install" || s.ModID != "fabric" {
		t.Errorf("suggestion = {%q %q}, want {install fabric}", s.Action, s.ModID)
	}
	if s.ModName != "Fabric API" {
		t.Errorf("mod_name = %q, want Fabric API", s.ModName)
	}
	if len(s.RequiredBy) != 2 || s.RequiredBy[0] != "AppleSkin" || s.RequiredBy[1] != "Sodium" {
		t.Errorf("required_by = %+v, want [AppleSkin Sodium]", s.RequiredBy)
	}
	// Both distinct version constraints survive; the trailing "." is stripped.
	if len(s.Requirements) != 2 || s.Requirements[0] != "any version" {
		t.Errorf("requirements = %+v, want [any version, version 0.100.0 or later]", s.Requirements)
	}
	if !strings.Contains(mc.Summary, "Fabric API") {
		t.Errorf("summary = %q, want it to name Fabric API", mc.Summary)
	}
}

// Some loader builds print the unmet-dependency listing without a solution
// block. The dependency must still be recovered from the "which is missing!"
// lines alone.
func TestConflictDetectorParsesMissingDepWithoutSolutionBlock(t *testing.T) {
	lines := []string{
		"[13:56:21] [main/ERROR]: Incompatible mods found!",
		"net.fabricmc.loader.impl.FormattedException: Some of your mods are incompatible with the game or each other!",
		"More details:",
		"\t - Mod 'Cloth Config' (cloth-config) 15.0.128 requires version 0.90.0 or later of fabric, which is missing!",
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
	if mc.Kind != "missing_dependency" {
		t.Errorf("kind = %q, want missing_dependency", mc.Kind)
	}
	if len(mc.Suggestions) != 1 || mc.Suggestions[0].ModID != "fabric" {
		t.Fatalf("suggestions = %+v, want one for fabric", mc.Suggestions)
	}
	if got := mc.Suggestions[0].RequiredBy; len(got) != 1 || got[0] != "Cloth Config" {
		t.Errorf("required_by = %+v, want [Cloth Config]", got)
	}
}

// A block that mixes a missing dependency with a genuine clash is still an
// "incompatible" conflict: disabling a mod remains part of the fix, so the
// panel must not narrow it to an install-only flow.
func TestConflictDetectorMixedBlockStaysIncompatible(t *testing.T) {
	lines := []string{
		"[13:56:21] [main/ERROR]: Incompatible mods found!",
		"A potential solution has been determined, this may resolve your problem:",
		"\t - Install fabric, any version.",
		"\t - Replace mod 'Async' (async) 0.2.2 with any version that is compatible with:",
		"\t\t - moonrise, any version",
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
	if mc.Kind != "incompatible" {
		t.Errorf("kind = %q, want incompatible", mc.Kind)
	}
	if len(mc.Suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", mc.Suggestions)
	}
}

// "minecraft" and "java" name the platform, not a downloadable mod. Offering to
// install them would be a dead end, so they must never become suggestions.
func TestConflictDetectorSkipsUninstallableDeps(t *testing.T) {
	lines := []string{
		"[13:56:21] [main/ERROR]: Incompatible mods found!",
		"A potential solution has been determined, this may resolve your problem:",
		"\t - Install minecraft, version 1.20.1.",
		"\t - Install java, version 21 or later.",
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
	if len(mc.Suggestions) != 0 {
		t.Errorf("suggestions = %+v, want none", mc.Suggestions)
	}
	if mc.Kind != "incompatible" {
		t.Errorf("kind = %q, want incompatible (no installable fix)", mc.Kind)
	}
}

// A conflict detected without any parseable suggestion lines must still
// marshal suggestions/raw as [] — the web maps over both (a null here crashed
// the server page after a failed version migration).
func TestConflictBuildNeverMarshalsNullArrays(t *testing.T) {
	var d conflictDetector
	mc := d.build()
	if mc.Suggestions == nil || mc.Raw == nil {
		t.Fatalf("nil arrays in built conflict: %+v", mc)
	}
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"suggestions":null`, `"raw":null`} {
		if strings.Contains(string(data), bad) {
			t.Errorf("marshalled conflict contains %s: %s", bad, data)
		}
	}

	var mx mixinCrashDetector
	mc = mx.build(nil)
	if mc.Suggestions == nil || mc.Raw == nil {
		t.Fatalf("nil arrays in built mixin conflict: %+v", mc)
	}
}
