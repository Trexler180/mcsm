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
