//go:build linux

package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Sharing a server directory makes what the agent owns group-readable and
// -writable, with directories passing the group on, and leaves the agent-only
// directories alone.
func TestShareDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "world", "region"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "mcsm-backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ShareDir(dir, os.Getgid()); err != nil {
		t.Fatal(err)
	}
	props, _ := os.Stat(filepath.Join(dir, "server.properties"))
	if props.Mode().Perm() != 0o664 {
		t.Fatalf("server.properties mode %v", props.Mode().Perm())
	}
	region, _ := os.Stat(filepath.Join(dir, "world", "region"))
	if region.Mode().Perm()&0o070 != 0o070 || region.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("world/region mode %v", region.Mode())
	}
	backups, _ := os.Stat(filepath.Join(dir, "mcsm-backups"))
	if backups.Mode().Perm() != 0o700 {
		t.Fatalf("mcsm-backups was shared: %v", backups.Mode().Perm())
	}
}

func TestTraversable(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "servers"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The test's own temp directories are 0700; open them as a host would
	// open the path above the server root.
	for _, d := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Another user (uid/gid that own nothing here) cannot get past "locked".
	if path, ok := traversable(filepath.Join(locked, "servers"), 65533, 65533); ok || path != filepath.Join(locked, "servers") && path != locked {
		t.Fatalf("traversable through a 0700 directory: %q %v", path, ok)
	}
	if err := os.Chmod(locked, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(locked, "servers"), 0o711); err != nil {
		t.Fatal(err)
	}
	if _, ok := traversable(filepath.Join(locked, "servers"), 65533, 65533); !ok {
		t.Fatal("o+x directories should be traversable")
	}
}

// The find expression reclaim runs as the server user, run here as the test's
// own user: what that user kept to itself (the way Java saves level.dat, or
// makes a temporary directory) gains group access; the agent's own
// directories and anything behind a symlink are left alone.
func TestReclaimArgs(t *testing.T) {
	if _, err := exec.LookPath("find"); err != nil {
		t.Skip("find is not installed")
	}
	dir, outside := t.TempDir(), t.TempDir()
	write := func(path string, mode os.FileMode) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	level := write(filepath.Join(dir, "world", "level.dat"), 0o600)
	shared := write(filepath.Join(dir, "server.properties"), 0o664)
	tmpDir := filepath.Join(dir, "world", "tmp")
	inTmp := write(filepath.Join(tmpDir, "chunk"), 0o600)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runState := write(filepath.Join(dir, ".mcsm-run", "state.json"), 0o600)
	backup := write(filepath.Join(dir, "mcsm-backups", "b.zip"), 0o600)
	secret := write(filepath.Join(outside, "secret"), 0o600)
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("find", reclaimArgs(dir, os.Getuid())...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("find: %v: %s", err, out)
	}

	for path, want := range map[string]os.FileMode{
		level: 0o660, inTmp: 0o660, tmpDir: 0o770, shared: 0o664,
		runState: 0o600, backup: 0o600, secret: 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %v, want %v", path, got, want)
		}
	}
}
