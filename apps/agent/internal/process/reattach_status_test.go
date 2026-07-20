package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reattach status inference must find "Done (" even when it scrolled past
// the seeded tail window — the stuck-"starting" bug that froze stats recording
// after every agent deploy under a long-running server.
func TestLogContainsDoneBeyondTailWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.log")

	var b strings.Builder
	b.WriteString("[12:00:00] [Server thread/INFO]: Done (34.5s)! For help, type \"help\"\n")
	// Bury the marker under far more chatter than the ring keeps.
	for i := 0; i < ringCapacity*3; i++ {
		b.WriteString("[12:34:56] [Server thread/INFO]: Player fell from a high place\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// The tail the ring would seed no longer contains the marker…
	if got := inferStatus(readLastLines(path, ringCapacity)); got != StatusStarting {
		t.Fatalf("tail inference = %v, want starting (marker must be out of window for this test)", got)
	}
	// …but the full-log scan does.
	if !logContainsDone(path) {
		t.Fatal("logContainsDone should find the marker beyond the tail window")
	}

	// A log with no marker at all stays negative.
	noDone := filepath.Join(dir, "nodone.log")
	if err := os.WriteFile(noDone, []byte("[12:00:00] [INFO]: Loading libraries\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if logContainsDone(noDone) {
		t.Fatal("logContainsDone must not match a log without the marker")
	}
	if logContainsDone(filepath.Join(dir, "missing.log")) {
		t.Fatal("logContainsDone must be false for a missing file")
	}
}
