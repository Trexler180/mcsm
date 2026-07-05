package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcsm/api/internal/agent"
)

func TestWithoutImportSettingsPreservesUnrelatedSettings(t *testing.T) {
	settings := json.RawMessage(`{"import":{"jar_file":"fabric-server-launch.jar","no_install":true},"motd":"hello"}`)

	got, changed := withoutImportSettings(settings)
	if !changed {
		t.Fatal("import settings were not removed")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if _, ok := decoded["import"]; ok {
		t.Fatal("import settings remain after conversion")
	}
	if decoded["motd"] != "hello" {
		t.Fatalf("unrelated setting = %#v, want hello", decoded["motd"])
	}
}

func TestWithoutImportSettingsLeavesManagedServerUntouched(t *testing.T) {
	settings := json.RawMessage(`{"motd":"hello"}`)
	got, changed := withoutImportSettings(settings)
	if changed {
		t.Fatal("managed settings reported as changed")
	}
	if string(got) != string(settings) {
		t.Fatalf("settings changed: %s", got)
	}
}

func TestStopForRuntimeChangeWaitsForOffline(t *testing.T) {
	var stopped atomic.Bool
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agent/v1/servers/server-1/stop":
			stopped.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})
		case r.Method == http.MethodGet && r.URL.Path == "/agent/v1/servers/server-1/status":
			status := "running"
			if stopped.Load() {
				status = "offline"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
		default:
			http.NotFound(w, r)
		}
	}))
	defer agentServer.Close()

	c := &agent.Client{BaseURL: agentServer.URL, Token: "test", HTTP: agentServer.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := stopForRuntimeChange(ctx, c, "server-1"); err != nil {
		t.Fatalf("stopForRuntimeChange: %v", err)
	}
	if !stopped.Load() {
		t.Fatal("running server was not stopped")
	}
}

// A crashed (or startup-failed) server keeps its diagnostic status until the
// next start — it never becomes "offline". The process is already dead, so a
// runtime change must proceed instead of waiting out the stop deadline.
func TestStopForRuntimeChangeAcceptsDeadStatuses(t *testing.T) {
	for _, status := range []string{"offline", "crashed", "startup_failure"} {
		t.Run(status, func(t *testing.T) {
			var stopCalls atomic.Int32
			agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/agent/v1/servers/server-1/stop":
					stopCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})
				case r.Method == http.MethodGet && r.URL.Path == "/agent/v1/servers/server-1/status":
					_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
				default:
					http.NotFound(w, r)
				}
			}))
			defer agentServer.Close()

			c := &agent.Client{BaseURL: agentServer.URL, Token: "test", HTTP: agentServer.Client()}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := stopForRuntimeChange(ctx, c, "server-1"); err != nil {
				t.Fatalf("stopForRuntimeChange with status %q: %v", status, err)
			}
			if n := stopCalls.Load(); n != 0 {
				t.Fatalf("dead server received %d stop requests", n)
			}
		})
	}
}
