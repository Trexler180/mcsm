package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyServerPortCreatesFile(t *testing.T) {
	dir := t.TempDir()

	changed, previous, err := ApplyServerPort(dir, 25570)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || previous != 0 {
		t.Fatalf("changed=%v previous=%d, want true/0", changed, previous)
	}
	got := readFile(t, filepath.Join(dir, PropertiesFile))
	if !strings.Contains(got, "server-port=25570") {
		t.Errorf("created file missing the port:\n%s", got)
	}
	if port, ok := ReadServerPort(dir); !ok || port != 25570 {
		t.Errorf("ReadServerPort = %d, %v; want 25570, true", port, ok)
	}
}

func TestApplyServerPortRewritesExisting(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, PropertiesFile),
		"#Minecraft server properties\nmotd=Hello\nserver-port=25565\nquery.port=25565\nrcon.port=25575\n")

	changed, previous, err := ApplyServerPort(dir, 25570)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || previous != 25565 {
		t.Fatalf("changed=%v previous=%d, want true/25565", changed, previous)
	}

	got := readFile(t, filepath.Join(dir, PropertiesFile))
	for _, want := range []string{
		"#Minecraft server properties",
		"motd=Hello",
		"server-port=25570",
		// query.port was tracking the server port, so it follows.
		"query.port=25570",
		// rcon has its own port and is none of our business.
		"rcon.port=25575",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q:\n%s", want, got)
		}
	}
}

func TestApplyServerPortLeavesIndependentQueryPort(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, PropertiesFile), "server-port=25565\nquery.port=25599\n")

	if _, _, err := ApplyServerPort(dir, 25570); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, PropertiesFile))
	if !strings.Contains(got, "query.port=25599") {
		t.Errorf("a query port set apart from the server port should be left alone:\n%s", got)
	}
}

func TestApplyServerPortAppendsMissingKey(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, PropertiesFile), "motd=Hello\n")

	changed, previous, err := ApplyServerPort(dir, 25570)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || previous != 0 {
		t.Fatalf("changed=%v previous=%d, want true/0", changed, previous)
	}
	got := readFile(t, filepath.Join(dir, PropertiesFile))
	if !strings.Contains(got, "motd=Hello") || !strings.Contains(got, "server-port=25570") {
		t.Errorf("unexpected result:\n%s", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("appending the port should not leave a blank line:\n%q", got)
	}
}

func TestApplyServerPortNoOpWhenAlreadyCorrect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PropertiesFile)
	original := "server-port=25570\nmotd=Hello\n"
	write(t, path, original)

	changed, previous, err := ApplyServerPort(dir, 25570)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a file that already names the port should not be rewritten")
	}
	if previous != 25570 {
		t.Errorf("previous = %d, want 25570", previous)
	}
	if got := readFile(t, path); got != original {
		t.Errorf("file changed:\n%q", got)
	}
}

func TestApplyServerPortPreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, PropertiesFile), "motd=Hello\r\nserver-port=25565\r\n")

	if _, _, err := ApplyServerPort(dir, 25570); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, PropertiesFile))
	if !strings.Contains(got, "server-port=25570\r\n") {
		t.Errorf("CRLF line endings should survive the edit: %q", got)
	}
}

func TestApplyServerPortRejectsInvalidPort(t *testing.T) {
	dir := t.TempDir()
	for _, port := range []int{0, -1, 70000} {
		if _, _, err := ApplyServerPort(dir, port); err == nil {
			t.Errorf("port %d: expected an error", port)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, PropertiesFile)); !os.IsNotExist(err) {
		t.Error("a rejected port should not have created the file")
	}
}

func TestReadServerPortMissing(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadServerPort(dir); ok {
		t.Error("no file should read as no port")
	}
	write(t, filepath.Join(dir, PropertiesFile), "motd=Hello\n")
	if _, ok := ReadServerPort(dir); ok {
		t.Error("no server-port key should read as no port")
	}
}

// With isolation on, the server's own code can swap server.properties for a
// symlink to the panel's secrets. Applying the port before a start must not
// read the target and copy it into the server directory.
func TestApplyServerPortRefusesASymlinkedFile(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(secret, []byte("JWT_SECRET=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, PropertiesFile)
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if _, _, err := ApplyServerPort(dir, 25570); err == nil {
		t.Fatal("applied the port through a symlinked server.properties")
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced with a copy of what it pointed at")
	}
	if got, _ := os.ReadFile(secret); string(got) != "JWT_SECRET=hunter2\n" {
		t.Fatalf("the secret file was changed: %q", got)
	}
}
