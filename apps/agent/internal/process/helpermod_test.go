package process

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcsm/agent/internal/helperjar"
	"github.com/mcsm/agent/internal/helpermod"
)

func helperPath(dir string) string {
	return filepath.Join(dir, "mods", helperjar.FileName)
}

func TestEnsureHelperModInstallsForFabric(t *testing.T) {
	dir := t.TempDir()

	note := ensureHelperMod(dir, "fabric", "26.2", true, nil)
	if note == "" {
		t.Error("expected a console note reporting the install")
	}

	data, err := os.ReadFile(helperPath(dir))
	if err != nil {
		t.Fatalf("helper jar not installed: %v", err)
	}
	if len(data) != helperjar.Size() {
		t.Errorf("installed jar is %d bytes, want %d", len(data), helperjar.Size())
	}
}

func TestEnsureHelperModIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	ensureHelperMod(dir, "fabric", "26.2", true, nil)
	info, err := os.Stat(helperPath(dir))
	if err != nil {
		t.Fatalf("stat after install: %v", err)
	}

	// A second start with the jar already current must not rewrite it, and must
	// not clutter the console with a note about work it did not do.
	if note := ensureHelperMod(dir, "fabric", "26.2", true, nil); note != "" {
		t.Errorf("second install produced note %q, want silence", note)
	}

	again, err := os.Stat(helperPath(dir))
	if err != nil {
		t.Fatalf("stat after second call: %v", err)
	}
	if !again.ModTime().Equal(info.ModTime()) {
		t.Error("jar was rewritten despite already being current")
	}
}

func TestEnsureHelperModReplacesStaleJar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath(dir), []byte("an old build"), 0o644); err != nil {
		t.Fatal(err)
	}

	note := ensureHelperMod(dir, "fabric", "26.2", true, nil)
	if note == "" {
		t.Error("expected a note reporting the update")
	}

	data, err := os.ReadFile(helperPath(dir))
	if err != nil {
		t.Fatalf("read jar: %v", err)
	}
	if len(data) != helperjar.Size() {
		t.Error("stale jar was not replaced with the embedded build")
	}
}

// Disabling must actually delete the jar. Leaving it in place would mean the
// panel says "off" while Fabric keeps loading the mod.
func TestEnsureHelperModRemovesWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	ensureHelperMod(dir, "fabric", "26.2", true, nil)

	note := ensureHelperMod(dir, "fabric", "26.2", false, nil)
	if note == "" {
		t.Error("expected a note reporting the removal")
	}
	if _, err := os.Stat(helperPath(dir)); !os.IsNotExist(err) {
		t.Error("jar still present after being disabled")
	}
}

func TestEnsureHelperModDisabledIsQuietWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if note := ensureHelperMod(dir, "fabric", "26.2", false, nil); note != "" {
		t.Errorf("got note %q for a no-op removal, want silence", note)
	}
}

// The mod is Fabric-only. Dropping it into a Paper or vanilla server would at
// best be ignored, so it must not be written at all.
func TestEnsureHelperModSkipsNonFabric(t *testing.T) {
	for _, platform := range []string{"paper", "purpur", "vanilla", "forge", "neoforge"} {
		dir := t.TempDir()
		if note := ensureHelperMod(dir, platform, "26.2", true, nil); note != "" {
			t.Errorf("platform %s: got note %q, want silence", platform, note)
		}
		if _, err := os.Stat(helperPath(dir)); !os.IsNotExist(err) {
			t.Errorf("platform %s: jar was installed on an unsupported platform", platform)
		}
	}
}

// Imported servers frequently have no platform recorded, so the directory is
// sniffed for Fabric's own markers instead of refusing outright.
func TestEnsureHelperModDetectsFabricWithoutDeclaredPlatform(t *testing.T) {
	cases := map[string]func(dir string) error{
		"launch jar": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "fabric-server-launch.jar"), []byte("x"), 0o644)
		},
		"libraries dir": func(dir string) error {
			return os.MkdirAll(filepath.Join(dir, "libraries", "net", "fabricmc"), 0o755)
		},
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := setup(dir); err != nil {
				t.Fatal(err)
			}
			ensureHelperMod(dir, "", "26.2", true, nil)
			if _, err := os.Stat(helperPath(dir)); err != nil {
				t.Errorf("fabric server was not detected: %v", err)
			}
		})
	}
}

func TestEnsureHelperModSkipsUnknownDirectory(t *testing.T) {
	dir := t.TempDir()
	ensureHelperMod(dir, "", "26.2", true, nil)
	if _, err := os.Stat(helperPath(dir)); !os.IsNotExist(err) {
		t.Error("installed into a directory with no evidence it is a Fabric server")
	}
}

// Fabric Loader treats an unsatisfied dependency as fatal, so installing a jar
// that declares "minecraft": "~26.2" onto a 1.21 server does not degrade — it
// stops the server booting. These cases guard the version check that prevents
// that, which is the difference between a missing feature and a dead server.
func TestEnsureHelperModRefusesIncompatibleVersions(t *testing.T) {
	for _, version := range []string{"1.20.1", "1.21.4", "1.21.11", "26.1", "26.3", ""} {
		dir := t.TempDir()

		note := ensureHelperMod(dir, "fabric", version, true, nil)

		if _, err := os.Stat(helperPath(dir)); !os.IsNotExist(err) {
			t.Errorf("mc %q: jar was installed onto an incompatible server", version)
		}
		if note == "" {
			t.Errorf("mc %q: skipping silently leaves the user with no idea why nothing happened", version)
		}
	}
}

// The embedded build declares the exact versions it was compiled and tested
// against — no prefix matching, no range algebra. A patch release it has never
// seen is served by the published index instead, which is the whole point of
// resolving builds over the network: new versions no longer need a new agent.
func TestEmbeddedBuildMatchesExactVersionsOnly(t *testing.T) {
	dir := t.TempDir()
	ensureHelperMod(dir, "fabric", "26.2", true, nil)
	if _, err := os.Stat(helperPath(dir)); err != nil {
		t.Fatalf("the embedded build should install on its declared version: %v", err)
	}

	other := t.TempDir()
	ensureHelperMod(other, "fabric", "26.2.1", true, nil)
	if _, err := os.Stat(helperPath(other)); !os.IsNotExist(err) {
		t.Error("embedded-only resolution installed onto a version it does not declare")
	}
}

// A server downgraded after the mod was installed would otherwise carry a jar
// that breaks its very next start, so the stale copy has to be cleared.
func TestEnsureHelperModRemovesJarAfterDowngrade(t *testing.T) {
	dir := t.TempDir()
	ensureHelperMod(dir, "fabric", "26.2", true, nil)
	if _, err := os.Stat(helperPath(dir)); err != nil {
		t.Fatalf("precondition: jar not installed: %v", err)
	}

	note := ensureHelperMod(dir, "fabric", "1.21.4", true, nil)
	if _, err := os.Stat(helperPath(dir)); !os.IsNotExist(err) {
		t.Error("jar survived a downgrade to an incompatible version")
	}
	if note == "" {
		t.Error("expected a note explaining the removal")
	}
}

// A transient failure to reach the index must not delete a mod that is
// currently working. Losing telemetry for one start is recoverable; deleting a
// user's installed mod because DNS hiccuped is not.
func TestEnsureHelperModKeepsExistingJarWhenResolveFails(t *testing.T) {
	dir := t.TempDir()
	ensureHelperMod(dir, "fabric", "26.2", true, nil)
	if _, err := os.Stat(helperPath(dir)); err != nil {
		t.Fatalf("precondition: jar not installed: %v", err)
	}

	// An index URL that cannot resolve, and an embedded build that does not
	// cover this version, so the resolver reports failure rather than "nothing".
	broken := &helpermod.Resolver{
		IndexURL:    "https://127.0.0.1:1/index.json",
		MaxProtocol: 1,
		HTTPClient:  &http.Client{Timeout: time.Second},
	}

	note := ensureHelperMod(dir, "fabric", "26.2", true, broken)

	if _, err := os.Stat(helperPath(dir)); err != nil {
		t.Error("a working jar was deleted because the index was unreachable")
	}
	if note == "" {
		t.Error("expected a warning explaining the failed check")
	}
}

func TestIsManagedMod(t *testing.T) {
	if !IsManagedMod(helperjar.FileName) {
		t.Error("the embedded jar name must be recognised as manager-owned")
	}
	if !IsManagedMod(filepath.Join("mods", helperjar.FileName)) {
		t.Error("a full path to the jar must be recognised")
	}
	if IsManagedMod("some-other-mod.jar") {
		t.Error("a user mod must not be treated as manager-owned")
	}
}
