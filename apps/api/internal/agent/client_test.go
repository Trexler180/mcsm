package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func clientFor(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "test", HTTP: srv.Client()}
}

func TestStartConfigIncludesSelectedLoader(t *testing.T) {
	loader := " 0.19.3 "
	cfg := StartConfig("server", "java", nil, "fabric", "26.1.2", &loader, 1024, 2048)
	if got := cfg["loader_version"]; got != "0.19.3" {
		t.Fatalf("loader_version = %#v, want 0.19.3", got)
	}

	cfg = StartConfig("server", "java", nil, "vanilla", "26.1.2", nil, 1024, 2048)
	if _, ok := cfg["loader_version"]; ok {
		t.Fatal("nil loader_version was included")
	}
}

func TestCheckErrorMessages(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"valid error json", http.StatusInternalServerError, `{"error":"disk full"}`, "agent: disk full"},
		{"html body", http.StatusBadGateway, `<html>nginx 502</html>`, "agent: HTTP 502"},
		{"empty body", http.StatusInternalServerError, ``, "agent: HTTP 500"},
		{"json without error field", http.StatusNotFound, `{"message":"nope"}`, "agent: HTTP 404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := c.StopServer(context.Background(), "s1", true, 5)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("got %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckErrorPassesOn2xx(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"stopping"}`))
	})
	if err := c.StopServer(context.Background(), "s1", true, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStatusSurfacesDecodeError(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status": "onli`)) // truncated JSON
	})
	if _, err := c.GetStatus(context.Background(), "s1"); err == nil {
		t.Fatal("truncated status JSON did not produce an error")
	}
}

func TestGetStatusSurfacesAgentError(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	_, err := c.GetStatus(context.Background(), "s1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("agent error not surfaced, got %v", err)
	}
}

func TestGetServerStatsDecodesEmbeddedVitals(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"status":"online",
			"cpu_percent":12.5,
			"ram_used_mb":1024,
			"ram_total_mb":8192,
			"players":[],
			"vitals":{
				"linked":true,
				"tps":{"m1":19.9,"m5":19.8,"m15":19.7},
				"mspt":{"avg":32.1,"p50":30,"p95":45,"p99":50,"max":60}
			}
		}`))
	})

	stats, err := c.GetServerStats(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetServerStats: %v", err)
	}
	if stats.Vitals == nil || !stats.Vitals.Linked || stats.Vitals.TPS == nil || stats.Vitals.MSPT == nil {
		t.Fatalf("embedded vitals were not decoded: %#v", stats.Vitals)
	}
	if stats.Vitals.TPS.M1 != 19.9 || stats.Vitals.MSPT.P95 != 45 {
		t.Fatalf("unexpected vitals: %#v", stats.Vitals)
	}
}
