//go:build linux

package isolation

import (
	"os"
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
