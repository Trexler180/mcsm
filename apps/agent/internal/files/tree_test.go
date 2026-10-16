package files

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSized(t *testing.T, base, rel string, size int) {
	t.Helper()
	p := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func treeFixture(t *testing.T) string {
	base := t.TempDir()
	writeSized(t, base, "server.jar", 100)
	writeSized(t, base, "world/level.dat", 10)
	writeSized(t, base, "world/region/r.0.0.mca", 1000)
	writeSized(t, base, "world/region/r.0.1.mca", 2000)
	writeSized(t, base, "world_nether/DIM-1/region/r.0.0.mca", 500)
	return base
}

func TestListTreeSizesTotalEveryChild(t *testing.T) {
	tree, err := ListTreeWith(treeFixture(t), "/", TreeOptions{MaxDepth: 2, Sizes: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"server.jar": 100, "world": 3010, "world_nether": 500}
	for k, v := range want {
		if tree.Sizes[k] != v {
			t.Fatalf("Sizes[%q] = %d, want %d (all: %v)", k, tree.Sizes[k], v, tree.Sizes)
		}
	}
	// Only what depth 2 allows is listed, even though everything was walked.
	for _, e := range tree.Entries {
		if e.Path != "server.jar" && e.Path != "world/level.dat" {
			t.Fatalf("listed %q beyond depth 2", e.Path)
		}
	}
	if len(tree.Entries) != 2 {
		t.Fatalf("entries = %+v, want server.jar and world/level.dat", tree.Entries)
	}
}

func TestListTreeSizesSurviveTheEntryCap(t *testing.T) {
	tree, err := ListTreeWith(treeFixture(t), "/", TreeOptions{MaxEntries: 1, Sizes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !tree.Truncated || len(tree.Entries) != 1 {
		t.Fatalf("truncated = %v, entries = %d; want the cap applied", tree.Truncated, len(tree.Entries))
	}
	if tree.Sizes["world"] != 3010 {
		t.Fatalf("Sizes[world] = %d, want 3010 past the cap", tree.Sizes["world"])
	}
}

func TestListTreeWithoutSizesIsUnchanged(t *testing.T) {
	tree, err := ListTree(treeFixture(t), "/", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Sizes != nil {
		t.Fatalf("sizes reported without being asked: %v", tree.Sizes)
	}
	if len(tree.Entries) != 2 {
		t.Fatalf("entries = %+v, want the two within depth 2", tree.Entries)
	}
}
