package process

import "testing"

// The canonical case: a broken mixin (missing class) aborts startup. Fabric
// blames the entrypoint owner (c2me), but the real culprit named by Mixin is
// pingviewer — which is what we must attribute and offer to disable.
func TestMixinCrashDetectorAttributesCulpritNotEntrypoint(t *testing.T) {
	lines := []string{
		"[18:19:34] [main/ERROR]: Mixin prepare for mod pingviewer failed preparing com.example.pingviewer.mixin.ExampleMixin in pingviewer.mixins.json: org.spongepowered.asm.mixin.transformer.throwables.InvalidMixinException The specified mixin 'pingviewer.mixin.com.example.pingviewer.mixin.ExampleMixin' was not found",
		"org.spongepowered.asm.mixin.transformer.throwables.InvalidMixinException: The specified mixin 'pingviewer.mixin.com.example.pingviewer.mixin.ExampleMixin' was not found",
		"\tat org.spongepowered.asm.mixin.transformer.MixinInfo.<init>(MixinInfo.java:886)",
		"\tat net.fabricmc.loader.impl.launch.knot.Knot.launch(Knot.java:66)",
		"[18:19:34] [main/ERROR]: A mod crashed on startup!",
		"net.fabricmc.loader.impl.FormattedException: java.lang.RuntimeException: Could not execute entrypoint stage 'preLaunch' due to errors, provided by 'c2me' at 'com.ishland.c2me.PreLaunchHandler'!",
		"\tat net.fabricmc.loader.impl.FormattedException.ofLocalized(FormattedException.java:63)",
		"Caused by: org.spongepowered.asm.mixin.throwables.MixinApplyError: Mixin [pingviewer.mixins.json:com.example.pingviewer.mixin.ExampleMixin from mod pingviewer] from phase [DEFAULT] in config [pingviewer.mixins.json] FAILED during PREPARE",
	}

	var d mixinCrashDetector
	var mc *ModConflict
	fires := 0
	for _, l := range lines {
		if got := d.feed(l); got != nil {
			mc = got
			fires++
		}
	}

	if mc == nil {
		t.Fatal("expected a crash conflict, got nil")
	}
	if fires != 1 {
		t.Fatalf("expected exactly one detection, got %d", fires)
	}
	if mc.Kind != "crash" {
		t.Errorf("kind = %q, want crash", mc.Kind)
	}
	if len(mc.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %+v", len(mc.Suggestions), mc.Suggestions)
	}
	s := mc.Suggestions[0]
	if s.ModID != "pingviewer" {
		t.Errorf("attributed mod = %q, want pingviewer (not the c2me entrypoint owner)", s.ModID)
	}
	if s.Action != "remove" {
		t.Errorf("action = %q, want remove", s.Action)
	}
	// The originating mixin error should be folded into the raw log for context.
	foundOrigin := false
	for _, r := range mc.Raw {
		if contains(r, "Mixin prepare for mod pingviewer failed") {
			foundOrigin = true
			break
		}
	}
	if !foundOrigin {
		t.Errorf("raw log missing the originating mixin error: %v", mc.Raw)
	}
}

// When the culprit is named only in the cause chain after the headline, we
// still attribute it rather than giving up.
func TestMixinCrashDetectorAttributesFromCauseChain(t *testing.T) {
	lines := []string{
		"[18:19:34] [main/ERROR]: A mod crashed on startup!",
		"net.fabricmc.loader.impl.FormattedException: ... provided by 'c2me' ...",
		"Caused by: org.spongepowered.asm.mixin.throwables.MixinApplyError: Mixin [foo.mixins.json:com.foo.Mixin from mod brokenmod] from phase [DEFAULT] FAILED during APPLY",
	}

	var d mixinCrashDetector
	var mc *ModConflict
	for _, l := range lines {
		if got := d.feed(l); got != nil {
			mc = got
		}
	}

	if mc == nil {
		t.Fatal("expected a crash conflict, got nil")
	}
	if len(mc.Suggestions) != 1 || mc.Suggestions[0].ModID != "brokenmod" {
		t.Fatalf("expected brokenmod attribution, got %+v", mc.Suggestions)
	}
}

// A non-fatal mixin error with no startup crash must NOT raise a popup.
func TestMixinCrashDetectorIgnoresNonFatalMixinError(t *testing.T) {
	lines := []string{
		"[18:19:34] [main/ERROR]: Mixin apply for mod somemod failed applying com.some.Mixin in somemod.mixins.json: ...",
		"[18:19:40] [main/INFO]: Done (12.3s)! For help, type \"help\"",
	}
	var d mixinCrashDetector
	for _, l := range lines {
		if got := d.feed(l); got != nil {
			t.Fatalf("expected no detection without a startup crash, got %+v", got)
		}
	}
}

// A generic startup crash with no mixin attribution must not be misattributed
// to the entrypoint owner ("provided by 'X'").
func TestMixinCrashDetectorDoesNotBlameEntrypointOwner(t *testing.T) {
	lines := []string{
		"[18:19:34] [main/ERROR]: A mod crashed on startup!",
		"net.fabricmc.loader.impl.FormattedException: ... provided by 'c2me' at 'com.ishland.c2me.PreLaunchHandler'!",
		"\tat net.fabricmc.loader.impl.launch.knot.Knot.init(Knot.java:158)",
	}
	var d mixinCrashDetector
	for _, l := range lines {
		if got := d.feed(l); got != nil {
			t.Fatalf("expected no attribution from 'provided by', got %+v", got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
