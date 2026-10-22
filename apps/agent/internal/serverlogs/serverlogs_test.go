package serverlogs

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLog = `[12:00:00] [Server thread/INFO]: Starting minecraft server version 1.21.4
[12:00:01] [Server thread/WARN]: Can't keep up! Is the server overloaded? Running 2154ms or 43 ticks behind
[12:00:02] [Server thread/INFO]: <Steve> [Server thread/ERROR]: fake error typed in chat
[12:00:05] [Server thread/WARN]: Can't keep up! Is the server overloaded? Running 5012ms or 100 ticks behind
[12:00:06] [Server thread/ERROR]: Encountered an unexpected exception
java.lang.NullPointerException: null
	at net.minecraft.server.Foo.bar(Foo.java:12)
	at net.minecraft.server.Foo.baz(Foo.java:34)
[12:00:07] [Server thread/INFO]: Done (3.2s)!
[12:01:00 WARN]: Plugin Example v1.2 generated an exception while executing task 42
`

func writeLogs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "latest.log"), []byte(sampleLog), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(logs, "2026-10-12-1.log.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte("[09:00:00] [Server thread/INFO]: old run\n[09:00:01] [Server thread/INFO]: stopping\n"))
	_ = gz.Close()
	_ = f.Close()
	return dir
}

func TestListAndTail(t *testing.T) {
	dir := writeLogs(t)
	files, err := List(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("list: %v %v", files, err)
	}
	lines, truncated, err := Tail(dir, "latest.log", 2)
	if err != nil || len(lines) != 2 || !truncated || !strings.Contains(lines[1], "Plugin Example") {
		t.Fatalf("tail: %q %v %v", lines, truncated, err)
	}
	lines, truncated, err = Tail(dir, "2026-10-12-1.log.gz", 10)
	if err != nil || len(lines) != 2 || truncated || !strings.Contains(lines[1], "stopping") {
		t.Fatalf("gz tail: %q %v %v", lines, truncated, err)
	}
	for _, bad := range []string{"../secret.log", "logs/latest.log", "server.properties", ""} {
		if _, _, err := Tail(dir, bad, 10); err == nil {
			t.Errorf("%q was readable", bad)
		}
	}
}

func TestProblemsGroupAndIgnoreChat(t *testing.T) {
	dir := writeLogs(t)
	problems, err := Problems(dir, "latest.log", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 3 {
		t.Fatalf("%d problems: %+v", len(problems), problems)
	}
	first := problems[0]
	if first.Level != "ERROR" || first.Count != 1 || !strings.Contains(first.Sample, "Foo.baz") {
		t.Fatalf("the error should come first with its stack trace: %+v", first)
	}
	var lag *Problem
	for i := range problems {
		if strings.HasPrefix(problems[i].Message, "Can't keep up") {
			lag = &problems[i]
		}
		if strings.Contains(problems[i].Sample, "typed in chat") {
			t.Fatal("a chat line was counted as a problem")
		}
	}
	if lag == nil || lag.Count != 2 || lag.FirstAt != "12:00:01" || lag.LastAt != "12:00:05" {
		t.Fatalf("the two lag warnings were not grouped: %+v", lag)
	}
}

// A logs/ the server's code replaced with a symlink must not lead the agent
// outside the server directory.
func TestLogsSymlinkOutOfTheServerIsRefused(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "latest.log"), []byte("someone else's console\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "logs")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if files, err := List(dir); err == nil && len(files) > 0 {
		t.Fatalf("listed logs through a symlink: %v", files)
	}
	if r, err := open(dir, "latest.log"); err == nil {
		r.Close()
		t.Fatal("opened a log through a symlinked logs directory")
	}
}
