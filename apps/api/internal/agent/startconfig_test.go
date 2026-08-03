package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

func fabricServer(settings string) *store.Server {
	return &store.Server{
		ID:            "srv1",
		DirectoryPath: "/srv/servers/srv1",
		JavaBinary:    "java",
		Platform:      "fabric",
		MCVersion:     "26.2",
		RAMMbMin:      1024,
		RAMMbMax:      4096,
		Settings:      json.RawMessage(settings),
	}
}

func TestStartConfigCarriesHelperModWhenEnabled(t *testing.T) {
	cfg := StartConfigForServer(fabricServer(`{"helper_mod":true}`))

	if cfg["helper_mod"] != true {
		t.Fatalf("helper_mod missing from start config: %v", cfg)
	}
}

// The regression that broke production: a start path that omits helper_mod does
// not merely fail to enable the mod, it tells the agent the mod is disabled —
// and the agent responds by deleting the jar and dropping the link env vars.
func TestStartConfigOmitsHelperModWhenDisabled(t *testing.T) {
	cfg := StartConfigForServer(fabricServer(`{"helper_mod":false}`))

	if _, ok := cfg["helper_mod"]; ok {
		t.Fatalf("helper_mod should be absent when disabled: %v", cfg)
	}
}

func TestStartConfigDefaultsHelperModOnForPanelCreatedFabric(t *testing.T) {
	if cfg := StartConfigForServer(fabricServer(`{}`)); cfg["helper_mod"] != true {
		t.Errorf("a panel-created Fabric server should default the mod on: %v", cfg)
	}
}

func TestStartConfigDefaultsHelperModOffForImported(t *testing.T) {
	srv := fabricServer(`{"import":{"jar_file":"server.jar","no_install":true}}`)

	cfg := StartConfigForServer(srv)
	if _, ok := cfg["helper_mod"]; ok {
		t.Errorf("an imported server should not get the mod by default: %v", cfg)
	}
}

// no_install is the flag that stops the agent re-provisioning a runtime over
// somebody's existing files, so a start path that drops it is destructive, not
// merely lossy.
func TestStartConfigCarriesImportFlags(t *testing.T) {
	srv := fabricServer(`{"import":{"jar_file":"paper-1.20.jar","no_install":true}}`)

	cfg := StartConfigForServer(srv)
	if cfg["no_install"] != true {
		t.Errorf("no_install missing: %v", cfg)
	}
	if cfg["jar_file"] != "paper-1.20.jar" {
		t.Errorf("jar_file missing: %v", cfg)
	}
}

// Without the port in the payload the agent has nothing to write into
// server.properties, so a server created on a custom port comes up on vanilla's
// 25565 while the panel keeps pinging the port the operator chose.
func TestStartConfigCarriesPort(t *testing.T) {
	srv := fabricServer(`{}`)
	srv.Port = 25570

	if cfg := StartConfigForServer(srv); cfg["port"] != 25570 {
		t.Errorf("port missing from start config: %v", cfg)
	}
}

func TestStartConfigOmitsUnsetPort(t *testing.T) {
	if cfg := StartConfigForServer(fabricServer(`{}`)); cfg["port"] != nil {
		t.Errorf("an unset port should leave server.properties alone: %v", cfg)
	}
}

func TestStartConfigRejectsIncompatibleVersions(t *testing.T) {
	srv := fabricServer(`{}`)
	srv.MCVersion = "1.21.4"

	if cfg := StartConfigForServer(srv); cfg["helper_mod"] != nil {
		t.Errorf("mod defaulted on for an incompatible version: %v", cfg)
	}
}

// TestEveryStartPathUsesTheSharedBuilder is the guard that actually prevents
// this class of bug returning.
//
// The original defect was not a wrong value, it was a call site that assembled
// the payload by hand and forgot two fields — and nothing failed, because a
// missing field is indistinguishable from a deliberate "off". Unit tests on the
// builder cannot catch that: the broken path never called the builder. So this
// asserts the structural property instead — outside this package, nobody calls
// the low-level StartConfig directly.
func TestEveryStartPathUsesTheSharedBuilder(t *testing.T) {
	root := filepath.Join("..", "..")

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This package legitimately defines and calls it.
		if filepath.Dir(path) == filepath.Join("..", "..", "internal", "agent") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Matches the low-level builder only: "agent.StartConfigForServer(" has
		// "ForServer" before the paren and so does not match.
		if strings.Contains(string(src), "agent.StartConfig(") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these build the agent start payload by hand instead of calling "+
			"agent.StartConfigForServer, so they will silently drop helper_mod and "+
			"the import flags:\n  %s", strings.Join(offenders, "\n  "))
	}
}
