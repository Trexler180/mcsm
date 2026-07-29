package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Runtime state lives under <serverRoot>/.mcsm-run/<id>/ and lets a freshly
// started agent re-discover and reattach to Minecraft processes that kept
// running across an agent restart or upgrade:
//
//	console.log  combined stdout+stderr; the JVM writes, the agent tails.
//	console.in   FIFO (unix); the agent writes console commands into it.
//	state.json   pid/pgid/config so reattach can identify and adopt the process.
//
// Centralising these under one directory (rather than inside each server's own
// folder) makes reattach a single directory scan on boot.

const runtimeDirName = ".mcsm-run"

// runState is the persisted descriptor for one running server.
type runState struct {
	ID        string      `json:"id"`
	PID       int         `json:"pid"`
	PGID      int         `json:"pgid"`
	StartedAt time.Time   `json:"started_at"`
	Directory string      `json:"directory"`
	Config    StartConfig `json:"config"`

	// LinkToken authenticates the helper mod's WebSocket link for this launch.
	// Persisted so a server that outlives an agent restart can re-authenticate
	// when it reconnects; cleared with the rest of the run state on stop.
	LinkToken string `json:"link_token,omitempty"`
}

func runtimeRoot(serverRoot string) string {
	return filepath.Join(serverRoot, runtimeDirName)
}

func runtimeDir(serverRoot, id string) string {
	return filepath.Join(runtimeRoot(serverRoot), id)
}

func consoleLogPath(serverRoot, id string) string {
	return filepath.Join(runtimeDir(serverRoot, id), "console.log")
}

func consoleFifoPath(serverRoot, id string) string {
	return filepath.Join(runtimeDir(serverRoot, id), "console.in")
}

func statePath(serverRoot, id string) string {
	return filepath.Join(runtimeDir(serverRoot, id), "state.json")
}

// writeRunState persists st atomically (write-temp then rename) so a crash mid
// write can't leave a half-written state.json that reattach would choke on.
//
// The file is 0600 rather than 0644 because it carries the launch token for the
// helper mod link. Only the agent ever reads this file, so restricting it costs
// nothing and keeps a live credential off a world-readable path.
func writeRunState(serverRoot string, st runState) error {
	dir := runtimeDir(serverRoot, st.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(serverRoot, st.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// WriteFile does not chmod an existing file, so a state.json created by an
	// older agent build would keep its 0644 mode without this.
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(serverRoot, st.ID))
}

func readRunState(serverRoot, id string) (*runState, error) {
	data, err := os.ReadFile(statePath(serverRoot, id))
	if err != nil {
		return nil, err
	}
	var st runState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// listRunStates returns every persisted run descriptor under the runtime root.
// Unreadable/garbage entries are skipped rather than failing the whole scan.
func listRunStates(serverRoot string) []runState {
	entries, err := os.ReadDir(runtimeRoot(serverRoot))
	if err != nil {
		return nil
	}
	out := make([]runState, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := readRunState(serverRoot, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *st)
	}
	return out
}

// clearRunState removes a server's whole runtime directory (state, fifo, log).
// Called on a clean stop or when a reattach finds the process already dead.
func clearRunState(serverRoot, id string) {
	_ = os.RemoveAll(runtimeDir(serverRoot, id))
}
