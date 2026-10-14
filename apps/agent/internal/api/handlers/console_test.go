package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/mcsm/agent/internal/process"
)

// The console of a stopped server says so and stays open, waiting for the
// next run, instead of closing (which made every client reconnect in a loop).
func TestConsoleStaysOpenWhileStopped(t *testing.T) {
	h := NewConsoleHandlers(process.NewManager(t.TempDir()), t.TempDir())
	r := chi.NewRouter()
	r.Get("/console/{id}", h.Console)
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/console/srv", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	var first map[string]any
	if err := wsjson.Read(ctx, conn, &first); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "status" || first["data"].(map[string]any)["status"] != "offline" {
		t.Fatalf("first frame %v", first)
	}
	// Nothing else arrives, and the server does not close the socket: the read
	// ends by our own deadline, not by a close frame.
	readCtx, readCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer readCancel()
	var next map[string]any
	err = wsjson.Read(readCtx, conn, &next)
	if err == nil {
		t.Fatalf("unexpected frame %v", next)
	}
	if status := websocket.CloseStatus(err); status != -1 {
		t.Fatalf("the server closed the console of a stopped server (%v)", status)
	}
}
