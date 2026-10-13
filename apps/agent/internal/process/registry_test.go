package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The directory registry outlives the agent process, so file and backup
// requests work right after an agent restart without the panel re-sending it.
func TestRegistrySurvivesRestart(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "survival")
	m := NewManager(root)
	m.RegisterDir("srv-1", dir)

	again := NewManager(root)
	if got, ok := again.GetDir("srv-1"); !ok || got != dir {
		t.Fatalf("after restart GetDir = %q, %v", got, ok)
	}
	again.Unregister("srv-1")
	if _, ok := NewManager(root).GetDir("srv-1"); ok {
		t.Fatal("an unregistered server came back after restart")
	}
}

// An entry edited to point outside the server root is dropped on load.
func TestRegistryRejectsDirectoriesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(runtimeRoot(root), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Dir(root)
	data := `{"evil":` + jsonString(outside) + `,"ok":` + jsonString(filepath.Join(root, "a")) + `}`
	if err := os.WriteFile(registryPath(root), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(root)
	if _, ok := m.GetDir("evil"); ok {
		t.Fatal("a directory outside the root was loaded from the registry")
	}
	if _, ok := m.GetDir("ok"); !ok {
		t.Fatal("a valid entry was dropped")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
