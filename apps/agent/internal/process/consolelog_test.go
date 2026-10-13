package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, path string, from, to int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := from; i < to; i++ {
		fmt.Fprintf(f, "[12:00:00 INFO]: line %d\n", i)
	}
}

func TestReadLastLinesReadsOnlyTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	writeLines(t, path, 0, 50_000)

	got := readLastLines(path, 3)
	want := []string{"[12:00:00 INFO]: line 49997", "[12:00:00 INFO]: line 49998", "[12:00:00 INFO]: line 49999"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	// A short file is returned whole, first line included.
	short := filepath.Join(t.TempDir(), "short.log")
	writeLines(t, short, 0, 2)
	if got := readLastLines(short, 10); len(got) != 2 || got[0] != "[12:00:00 INFO]: line 0" {
		t.Fatalf("short file: %q", got)
	}
}

func TestConsoleLogRotatesOnceCaughtUp(t *testing.T) {
	prev := maxConsoleLogBytes
	maxConsoleLogBytes = 1 << 10
	defer func() { maxConsoleLogBytes = prev }()

	path := filepath.Join(t.TempDir(), "console.log")
	writeLines(t, path, 0, 100)
	fi, _ := os.Stat(path)
	inst := &Instance{ID: "srv", logPath: path}

	// Not caught up: nothing happens yet, the unread lines must not be lost.
	if inst.checkConsoleLog(fi.Size()-10) != logUnchanged {
		t.Fatal("rotated before the tailer had read everything")
	}
	if inst.checkConsoleLog(fi.Size()) != logRestart {
		t.Fatal("an oversized, fully read log was not rotated")
	}
	if now, _ := os.Stat(path); now.Size() != 0 {
		t.Fatalf("live log is %d bytes after rotation", now.Size())
	}
	if old, err := os.Stat(previousLogPath(path)); err != nil || old.Size() != fi.Size() {
		t.Fatalf("previous generation: %v, %v", old, err)
	}
	// The writer keeps appending to the truncated file.
	writeLines(t, path, 100, 101)
	if got := readLastLines(path, 5); len(got) != 1 || got[0] != "[12:00:00 INFO]: line 100" {
		t.Fatalf("after rotation: %q", got)
	}
	// A log shorter than what was read was truncated underneath the tailer.
	if inst.checkConsoleLog(fi.Size()) != logRestart {
		t.Fatal("a shrunken log was not reported")
	}
}

func TestDoneMarkerSurvivesRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := os.WriteFile(previousLogPath(path), []byte("[INFO]: Done (3.2s)! For help, type \"help\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLines(t, path, 0, 3)
	if !logContainsDone(path) {
		t.Fatal("the online marker in the rotated generation was missed")
	}
}
