package files

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// makeZip writes a zip containing the given entries (path -> contents) and
// returns its path. A path ending in "/" is stored as a directory entry.
func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %q: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

func TestInspectWorldZipFindsWorldRoot(t *testing.T) {
	cases := []struct {
		name       string
		entries    map[string]string
		wantPrefix string
		wantName   string
	}{
		{
			name: "world at archive root",
			entries: map[string]string{
				"level.dat":           "dat",
				"region/r.0.0.mca":    "chunks",
				"data/scoreboard.dat": "scores",
				"playerdata/uuid.dat": "player",
			},
			wantPrefix: "",
			wantName:   "",
		},
		{
			name: "world wrapped in its own folder",
			entries: map[string]string{
				"My World/level.dat":        "dat",
				"My World/region/r.0.0.mca": "chunks",
			},
			wantPrefix: "My World/",
			wantName:   "My World",
		},
		{
			// A whole-server backup: the shallowest level.dat is the world, not
			// the archive root, and the server files beside it are ignored.
			name: "full server backup",
			entries: map[string]string{
				"server.properties":    "level-name=world",
				"mods/some.jar":        "jar",
				"world/level.dat":      "dat",
				"world/region/r.0.mca": "chunks",
			},
			wantPrefix: "world/",
			wantName:   "world",
		},
		{
			// Zipping a folder that already contained the zip's own name is
			// common; the inner level.dat still wins on depth.
			name: "doubly wrapped",
			entries: map[string]string{
				"Backup/SkyBlock/level.dat": "dat",
				"Backup/readme.txt":         "hi",
			},
			wantPrefix: "Backup/SkyBlock/",
			wantName:   "SkyBlock",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arc, err := InspectWorldZip(makeZip(t, tc.entries))
			if err != nil {
				t.Fatalf("InspectWorldZip: %v", err)
			}
			if arc.Prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q", arc.Prefix, tc.wantPrefix)
			}
			if arc.SuggestedName != tc.wantName {
				t.Errorf("suggested name = %q, want %q", arc.SuggestedName, tc.wantName)
			}
			if arc.Files == 0 || arc.Bytes == 0 {
				t.Errorf("files=%d bytes=%d, want both non-zero", arc.Files, arc.Bytes)
			}
		})
	}
}

func TestInspectWorldZipRejectsNonWorlds(t *testing.T) {
	if _, err := InspectWorldZip(makeZip(t, map[string]string{
		"mods/some.jar":     "jar",
		"server.properties": "x",
	})); err != ErrNoLevelDat {
		t.Fatalf("err = %v, want ErrNoLevelDat", err)
	}

	// Two worlds side by side: choosing one for the user would be a guess.
	if _, err := InspectWorldZip(makeZip(t, map[string]string{
		"survival/level.dat": "dat",
		"creative/level.dat": "dat",
	})); err != ErrMultipleWorlds {
		t.Fatalf("err = %v, want ErrMultipleWorlds", err)
	}

	if _, err := InspectWorldZip(filepath.Join(t.TempDir(), "missing.zip")); err == nil {
		t.Fatal("expected an error for a file that isn't a zip")
	}
}

func TestExtractWorldStripsPrefixAndIgnoresSiblings(t *testing.T) {
	base := t.TempDir()
	zipPath := makeZip(t, map[string]string{
		"server.properties":      "level-name=world",
		"mods/some.jar":          "jar",
		"world/level.dat":        "dat",
		"world/region/r.0.0.mca": "chunks",
		"world/data/raids.dat":   "raids",
	})
	arc, err := InspectWorldZip(zipPath)
	if err != nil {
		t.Fatalf("InspectWorldZip: %v", err)
	}
	if err := ExtractWorld(base, zipPath, arc, "Imported"); err != nil {
		t.Fatalf("ExtractWorld: %v", err)
	}

	for _, want := range []string{"level.dat", "region/r.0.0.mca", "data/raids.dat"} {
		if _, err := os.Stat(filepath.Join(base, "Imported", filepath.FromSlash(want))); err != nil {
			t.Errorf("expected %s in the extracted world: %v", want, err)
		}
	}
	// Files outside the world prefix must not follow it into the server root.
	for _, unwanted := range []string{"server.properties", "mods", "Imported/world"} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(unwanted))); err == nil {
			t.Errorf("%s was extracted but lies outside the world", unwanted)
		}
	}
	// No staging or parked directories left behind.
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 || entries[0].Name() != "Imported" {
		t.Errorf("server root = %v, want only the imported world", entries)
	}
}

func TestExtractWorldReplacesExistingWorld(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "world")
	if err := os.MkdirAll(filepath.Join(old, "region"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "stale.dat"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := makeZip(t, map[string]string{
		"level.dat":        "new",
		"region/r.0.0.mca": "chunks",
	})
	arc, err := InspectWorldZip(zipPath)
	if err != nil {
		t.Fatalf("InspectWorldZip: %v", err)
	}
	if err := ExtractWorld(base, zipPath, arc, "world"); err != nil {
		t.Fatalf("ExtractWorld: %v", err)
	}

	// Replacement is wholesale: nothing from the previous world survives.
	if _, err := os.Stat(filepath.Join(old, "stale.dat")); err == nil {
		t.Error("stale.dat from the replaced world is still present")
	}
	body, err := os.ReadFile(filepath.Join(old, "level.dat"))
	if err != nil || string(body) != "new" {
		t.Errorf("level.dat = %q (%v), want the uploaded copy", body, err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 {
		t.Errorf("server root = %v, want only the replaced world", entries)
	}
}

func TestExtractWorldRejectsTraversalEntries(t *testing.T) {
	base := t.TempDir()
	// The archive is a valid world with one entry trying to climb out of it.
	zipPath := makeZip(t, map[string]string{
		"level.dat":         "dat",
		"../../escaped.txt": "pwned",
	})
	arc, err := InspectWorldZip(zipPath)
	if err != nil {
		t.Fatalf("InspectWorldZip: %v", err)
	}
	if err := ExtractWorld(base, zipPath, arc, "world"); err == nil {
		t.Fatal("expected extraction to refuse a traversal entry")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escaped.txt")); err == nil {
		t.Fatal("traversal entry escaped the server directory")
	}
	// A refused extraction leaves nothing behind, staging included.
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Errorf("server root = %v, want it untouched", entries)
	}
}

func TestExtractWorldKeepsExistingWorldWhenExtractionFails(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "world", "level.dat"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := makeZip(t, map[string]string{
		"level.dat":     "replacement",
		"../escape.txt": "pwned",
	})
	arc, err := InspectWorldZip(zipPath)
	if err != nil {
		t.Fatalf("InspectWorldZip: %v", err)
	}
	if err := ExtractWorld(base, zipPath, arc, "world"); err == nil {
		t.Fatal("expected extraction to fail")
	}
	body, err := os.ReadFile(filepath.Join(base, "world", "level.dat"))
	if err != nil || string(body) != "original" {
		t.Fatalf("level.dat = %q (%v), want the original world intact", body, err)
	}
}

func TestCleanWorldName(t *testing.T) {
	ok := []string{"world", "My World", "world_nether", "Sky-Block 2", "wörld"}
	for _, name := range ok {
		if got, err := CleanWorldName(name); err != nil || got != name {
			t.Errorf("CleanWorldName(%q) = %q, %v; want it accepted", name, got, err)
		}
	}

	bad := []string{
		"", "   ", ".", "..", ".hidden", "world.", "a/b", `a\b`, "a:b", "a*b",
		"a?b", `a"b`, "a<b", "a>b", "a|b", "con", "NUL.txt", "aux",
	}
	for _, name := range bad {
		if _, err := CleanWorldName(name); err == nil {
			t.Errorf("CleanWorldName(%q) accepted a name it should reject", name)
		}
	}

	// Surrounding whitespace is a paste artifact, not a naming choice.
	if got, err := CleanWorldName("  world  "); err != nil || got != "world" {
		t.Errorf("CleanWorldName(padded) = %q, %v; want %q", got, err, "world")
	}
}

func TestWorldExists(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if ok, err := WorldExists(base, "world"); err != nil || !ok {
		t.Errorf("WorldExists(world) = %v, %v; want true", ok, err)
	}
	if ok, err := WorldExists(base, "missing"); err != nil || ok {
		t.Errorf("WorldExists(missing) = %v, %v; want false", ok, err)
	}
	// A plain file is not a world folder.
	if ok, err := WorldExists(base, "notes.txt"); err != nil || ok {
		t.Errorf("WorldExists(notes.txt) = %v, %v; want false", ok, err)
	}
}
