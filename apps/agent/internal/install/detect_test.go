package install

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectPaperServer(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "paper-1.21.4-123.jar"), "x")
	write(t, filepath.Join(dir, "server.properties"), "server-port=25570\nlevel-name=world\n")
	write(t, filepath.Join(dir, "eula.txt"), "eula=true\n")
	write(t, filepath.Join(dir, "world", "level.dat"), "x")
	write(t, filepath.Join(dir, "plugins", "EssentialsX.jar"), "x")

	d := Detect(dir)
	if d.Platform != "paper" {
		t.Errorf("platform = %q, want paper", d.Platform)
	}
	if d.JarFile != "paper-1.21.4-123.jar" {
		t.Errorf("jar = %q", d.JarFile)
	}
	if d.MCVersion != "1.21.4" {
		t.Errorf("version = %q, want 1.21.4", d.MCVersion)
	}
	if d.Port != 25570 {
		t.Errorf("port = %d, want 25570", d.Port)
	}
	if !d.EULA {
		t.Error("eula should be detected true")
	}
	if !d.HasWorld {
		t.Error("world should be detected")
	}
	if d.PluginCount != 1 {
		t.Errorf("plugin count = %d, want 1", d.PluginCount)
	}
}

func TestDetectFabricServer(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "fabric-server-launch.jar"), "x")
	write(t, filepath.Join(dir, "mods", "sodium.jar"), "x")
	write(t, filepath.Join(dir, "mods", "lithium.jar.disabled"), "x")

	d := Detect(dir)
	if d.Platform != "fabric" {
		t.Errorf("platform = %q, want fabric", d.Platform)
	}
	if d.JarFile != "fabric-server-launch.jar" {
		t.Errorf("jar = %q", d.JarFile)
	}
	if d.ModCount != 2 {
		t.Errorf("mod count = %d, want 2", d.ModCount)
	}
	if d.Port != 25565 {
		t.Errorf("default port = %d, want 25565", d.Port)
	}
}

func TestDetectGenericServerJar(t *testing.T) {
	// Paper renamed to server.jar, identified by its config marker.
	paper := t.TempDir()
	write(t, filepath.Join(paper, "server.jar"), "x")
	write(t, filepath.Join(paper, "config", "paper-global.yml"), "x")
	if d := Detect(paper); d.Platform != "paper" || d.JarFile != "server.jar" {
		t.Errorf("paper-by-config = %q/%q, want paper/server.jar", d.Platform, d.JarFile)
	}

	// server.jar with an empty mods dir is vanilla, not a modloader.
	vanilla := t.TempDir()
	write(t, filepath.Join(vanilla, "server.jar"), "x")
	if err := os.MkdirAll(filepath.Join(vanilla, "mods"), 0755); err != nil {
		t.Fatal(err)
	}
	if d := Detect(vanilla); d.Platform != "vanilla" {
		t.Errorf("empty-mods server.jar = %q, want vanilla", d.Platform)
	}
}

func TestLooksLikeServer(t *testing.T) {
	empty := t.TempDir()
	if LooksLikeServer(empty) {
		t.Error("empty dir should not look like a server")
	}
	withJar := t.TempDir()
	write(t, filepath.Join(withJar, "server.jar"), "x")
	if !LooksLikeServer(withJar) {
		t.Error("dir with a jar should look like a server")
	}
	withProps := t.TempDir()
	write(t, filepath.Join(withProps, "server.properties"), "server-port=25565\n")
	if !LooksLikeServer(withProps) {
		t.Error("dir with server.properties should look like a server")
	}
}
