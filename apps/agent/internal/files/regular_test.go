package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// symlinkOrSkip plants a symlink, skipping where the OS won't let the test
// make one (Windows without developer mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestReadRegularRefusesASymlinkOutOfTheDirectory(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(secret, []byte("JWT_SECRET=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	symlinkOrSkip(t, secret, filepath.Join(dir, "server.properties"))

	if data, err := ReadRegular(dir, "server.properties", 0); err == nil {
		t.Fatalf("read through a symlink out of the server directory: %q", data)
	}
}

func TestReadRegularRefusesASymlinkInsideTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, "real.txt", filepath.Join(dir, "link.txt"))
	if _, err := ReadRegular(dir, "link.txt", 0); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular", err)
	}
}

func TestWriteRegularAtomicNeverWritesThroughALink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(outside, []byte("KEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(dir, "server.properties"))

	if err := WriteRegularAtomic(dir, "server.properties", []byte("server-port=1\n"), 0o644); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "KEEP" {
		t.Fatalf("the file behind the link was changed: %q", got)
	}
}

func TestWriteRegularAtomicReplacesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.properties")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteRegularAtomic(dir, "server.properties", []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" {
		t.Fatalf("content = %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestCreateRegularIfMissingDoesNotFollowADanglingLink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planted")
	dir := t.TempDir()
	symlinkOrSkip(t, target, filepath.Join(dir, "eula.txt"))

	created, err := CreateRegularIfMissing(dir, "eula.txt", []byte("eula=true\n"), 0o644)
	if err != nil || created {
		t.Fatalf("created=%v err=%v; a planted name must be left alone", created, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("wrote through a dangling symlink")
	}
}

func TestMkdirAllInStaysInside(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(dir, "mods"))
	if err := MkdirAllIn(dir, filepath.Join("mods", "sub"), 0o755); err == nil {
		if _, statErr := os.Stat(filepath.Join(outside, "sub")); statErr == nil {
			t.Fatal("created a directory outside the server directory through a symlink")
		}
	}
}
