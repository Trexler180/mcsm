package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	st := runState{
		ID:        "srv1",
		PID:       4242,
		PGID:      4242,
		StartedAt: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
		Directory: filepath.Join(root, "servers", "srv1"),
		Config:    StartConfig{Directory: "servers/srv1", JarFile: "server.jar", JVMArgs: []string{"-Xmx2G"}},
	}
	if err := writeRunState(root, st); err != nil {
		t.Fatalf("writeRunState: %v", err)
	}

	got, err := readRunState(root, "srv1")
	if err != nil {
		t.Fatalf("readRunState: %v", err)
	}
	if got.PID != st.PID || got.Directory != st.Directory || got.Config.JarFile != "server.jar" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Config.JVMArgs) != 1 || got.Config.JVMArgs[0] != "-Xmx2G" {
		t.Fatalf("config not preserved: %+v", got.Config)
	}

	list := listRunStates(root)
	if len(list) != 1 || list[0].ID != "srv1" {
		t.Fatalf("listRunStates = %+v, want one srv1", list)
	}

	clearRunState(root, "srv1")
	if _, err := os.Stat(runtimeDir(root, "srv1")); !os.IsNotExist(err) {
		t.Fatalf("runtime dir should be gone after clear, stat err = %v", err)
	}
	if got := listRunStates(root); len(got) != 0 {
		t.Fatalf("listRunStates after clear = %+v, want empty", got)
	}
}

func TestListRunStatesSkipsGarbage(t *testing.T) {
	root := t.TempDir()
	// A directory with a corrupt state.json must be skipped, not fatal.
	bad := runtimeDir(root, "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := listRunStates(root); len(got) != 0 {
		t.Fatalf("expected garbage entry skipped, got %+v", got)
	}
}

func TestInferStatus(t *testing.T) {
	if s := inferStatus([]string{"[12:00:00] [Server thread/INFO]: Starting minecraft server"}); s != StatusStarting {
		t.Fatalf("no Done line should be starting, got %s", s)
	}
	if s := inferStatus([]string{
		"[12:00:00] [Server thread/INFO]: Preparing spawn area",
		`[12:00:05] [Server thread/INFO]: Done (5.123s)! For help, type "help"`,
	}); s != StatusOnline {
		t.Fatalf(`Done line should be online, got %s`, s)
	}
}

func TestReadLastLines(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "console.log")
	if err := os.WriteFile(p, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readLastLines(p, 3)
	if len(got) != 3 || got[0] != "c" || got[2] != "e" {
		t.Fatalf("readLastLines = %v, want [c d e]", got)
	}
	// CRLF normalization + missing-file safety.
	if err := os.WriteFile(p, []byte("x\r\ny\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLastLines(p, 10); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("CRLF handling = %v, want [x y]", got)
	}
	if got := readLastLines(filepath.Join(root, "nope.log"), 5); got != nil {
		t.Fatalf("missing file should return nil, got %v", got)
	}
}
