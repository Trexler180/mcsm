package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Every command typed into the console is reported before it is forwarded,
// and a command with a line break — several commands recorded as one — is
// refused.
func TestProxyConsoleReportsEachCommand(t *testing.T) {
	fa := &fakeAgent{}
	agentSrv := httptest.NewServer(fa.handler())
	defer agentSrv.Close()
	client := agentClientFor(t, agentSrv)

	var mu sync.Mutex
	var recorded []string
	onInput := func(_ context.Context, cmd string) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, cmd)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ProxyConsole(w, r, client, "srv1", nil, onInput)
	}))
	defer proxy.Close()

	conn, ctx := dialProxy(t, proxy)
	defer conn.CloseNow()

	smuggled := `{"type":"input","data":{"command":"say hi\nop someone"}}`
	plain := `{"type":"input","data":{"command":"list"}}`
	for _, frame := range []string{smuggled, plain} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return fa.got(plain) }, "the plain command never reached the agent")
	time.Sleep(20 * time.Millisecond)

	if fa.got(smuggled) {
		t.Fatal("a multi-line command was forwarded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 || recorded[0] != "list" {
		t.Fatalf("recorded %q, want only the forwarded command", recorded)
	}
}

func TestConsoleCommandParsing(t *testing.T) {
	for _, tc := range []struct {
		frame            string
		cmd              string
		isInput, forward bool
	}{
		{`{"type":"input","data":{"command":"list"}}`, "list", true, true},
		{`{"type":"resize"}`, "", false, true},
		{`not json`, "", false, true},
		{`{"type":"input","data":{"command":""}}`, "", true, false},
		{`{"type":"input","data":{"command":"a\rb"}}`, "a\rb", true, false},
	} {
		cmd, isInput, forward := consoleCommand(websocket.MessageText, []byte(tc.frame))
		if cmd != tc.cmd || isInput != tc.isInput || forward != tc.forward {
			t.Errorf("%s: got (%q, %v, %v)", tc.frame, cmd, isInput, forward)
		}
	}
}
