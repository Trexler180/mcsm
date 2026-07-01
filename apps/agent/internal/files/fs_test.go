package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustBeInside fails the test unless p is base itself or under base.
func mustBeInside(t *testing.T, base, p string) {
	t.Helper()
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("resolved path %q escapes base %q", p, base)
	}
}

func TestResolveContainsTraversal(t *testing.T) {
	base := t.TempDir()

	// Escape attempts must either error or clamp inside the base — Resolve
	// clamps via Clean("/"+path), so what matters is the invariant, not the
	// exact result.
	escapes := []string{
		"..",
		"../..",
		"../../etc/passwd",
		"a/../../x",
		"a/../../../x",
		"..\\..\\windows",
		"/../../x",
		"/etc/passwd",
		"C:\\Windows\\system32",
		"....//x",
	}
	for _, p := range escapes {
		got, err := Resolve(base, p)
		if err != nil {
			continue // rejected outright: fine
		}
		mustBeInside(t, base, got)
	}

	// Normal paths resolve inside the base.
	valid := []string{"", ".", "world/level.dat", "mods/some mod.jar", "a/b/c/d"}
	for _, p := range valid {
		got, err := Resolve(base, p)
		if err != nil {
			t.Fatalf("Resolve(%q) unexpectedly failed: %v", p, err)
		}
		mustBeInside(t, base, got)
	}
}

func TestResolveExisting(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "world")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "level.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveExisting(base, "world/level.dat")
	if err != nil {
		t.Fatalf("ResolveExisting failed on a real file: %v", err)
	}
	mustBeInside(t, base, got)

	if _, err := ResolveExisting(base, "does/not/exist"); err == nil {
		t.Fatal("ResolveExisting succeeded on a missing file")
	}
}

// TestResolveExistingSymlinkEscape verifies that a symlink inside the base
// pointing outside it is rejected after EvalSymlinks. Skips where symlink
// creation isn't permitted (default on Windows without dev mode).
func TestResolveExistingSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{base, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if _, err := ResolveExisting(base, "link/secret.txt"); err == nil {
		t.Fatal("ResolveExisting followed a symlink out of the base")
	}
}

func TestResolveForWrite(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Writing into an existing dir and a not-yet-existing dir both resolve
	// inside the base.
	for _, p := range []string{"configs/new.yml", "brand/new/dir/file.txt"} {
		got, err := ResolveForWrite(base, p)
		if err != nil {
			t.Fatalf("ResolveForWrite(%q) failed: %v", p, err)
		}
		mustBeInside(t, base, got)
	}

	// Traversal attempts obey the same invariant as Resolve.
	for _, p := range []string{"../evil.txt", "a/../../evil.txt"} {
		got, err := ResolveForWrite(base, p)
		if err != nil {
			continue
		}
		mustBeInside(t, base, got)
	}
}

// TestResolveForWriteSymlinkParentEscape verifies that writing through a
// symlinked parent directory that points outside the base is rejected.
func TestResolveForWriteSymlinkParentEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{base, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if _, err := ResolveForWrite(base, "link/evil.txt"); err == nil {
		t.Fatal("ResolveForWrite allowed writing through a symlink out of the base")
	}
}
