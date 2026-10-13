package lifecycle

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

func TestStopAndWaitWaitsForOffline(t *testing.T) {
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
	if err := StopAndWait(ctx, c, "server-1"); err != nil {
		t.Fatalf("StopAndWait: %v", err)
	}
	if !stopped.Load() {
		t.Fatal("running server was not stopped")
	}
}

// A crashed (or startup-failed) server keeps its diagnostic status until the
// next start — it never becomes "offline". The process is already dead, so a
// runtime change must proceed instead of waiting out the stop deadline.
func TestStopAndWaitAcceptsDeadStatuses(t *testing.T) {
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
			if err := StopAndWait(ctx, c, "server-1"); err != nil {
				t.Fatalf("StopAndWait with status %q: %v", status, err)
			}
			if n := stopCalls.Load(); n != 0 {
				t.Fatalf("dead server received %d stop requests", n)
			}
		})
	}
}
