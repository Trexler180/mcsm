package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mcsm/api/internal/agent"
)

// fakeAgent stands in for the per-host agent's console/metrics WebSockets.
// Console records every inbound message so a test can assert that forwarding
// stops after revocation; metrics streams ticks until the peer goes away.
type fakeAgent struct {
	mu       sync.Mutex
	received []string
}

func (f *fakeAgent) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
			OriginPatterns:     []string{"*"},
		})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()

		if strings.HasSuffix(r.URL.Path, "/metrics") {
			for {
				if err := c.Write(ctx, websocket.MessageText, []byte("tick")); err != nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}

		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.received = append(f.received, string(data))
			f.mu.Unlock()
		}
	})
}

func (f *fakeAgent) got(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.received {
		if m == s {
			return true
		}
	}
	return false
}

func agentClientFor(t *testing.T, srv *httptest.Server) *agent.Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return agent.New("http", u.Hostname(), port, "token")
}

func dialProxy(t *testing.T, proxy *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx := context.Background()
	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	return conn, ctx
}

// Console permission loss must stop forwarding the *next* browser→agent message
// and tear the connection down — not merely on the periodic timer.
func TestProxyConsoleStopsForwardingWhenRevoked(t *testing.T) {
	fa := &fakeAgent{}
	agentSrv := httptest.NewServer(fa.handler())
	defer agentSrv.Close()
	client := agentClientFor(t, agentSrv)

	var allowed atomic.Bool
	allowed.Store(true)
	check := func(context.Context) bool { return allowed.Load() }

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ProxyConsole(w, r, client, "srv1", check)
	}))
	defer proxy.Close()

	conn, ctx := dialProxy(t, proxy)
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageText, []byte("list")); err != nil {
		t.Fatalf("write while permitted: %v", err)
	}
	waitFor(t, func() bool { return fa.got("list") }, "agent never received permitted command")

	// Revoke, then send a command that must not reach the agent.
	allowed.Store(false)
	_ = conn.Write(ctx, websocket.MessageText, []byte("op everyone"))

	// The proxy should close our side; the forbidden command must never arrive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, err := conn.Read(ctx); err != nil {
			break // connection closed by proxy, as expected
		}
	}
	if fa.got("op everyone") {
		t.Fatal("forbidden command was forwarded to the agent after revocation")
	}
}

// Metrics has no inbound traffic, so revocation relies on the periodic re-check
// closing the socket. Drive it with a short interval instead of the 30s default.
func TestProxyMetricsClosesOnRevoke(t *testing.T) {
	prev := metricsRecheckInterval
	metricsRecheckInterval = 20 * time.Millisecond
	defer func() { metricsRecheckInterval = prev }()

	fa := &fakeAgent{}
	agentSrv := httptest.NewServer(fa.handler())
	defer agentSrv.Close()
	client := agentClientFor(t, agentSrv)

	var allowed atomic.Bool
	allowed.Store(true)
	check := func(context.Context) bool { return allowed.Load() }

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ProxyMetrics(w, r, client, "srv1", check)
	}))
	defer proxy.Close()

	conn, ctx := dialProxy(t, proxy)
	defer conn.CloseNow()

	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("expected a metrics frame while permitted: %v", err)
	}

	allowed.Store(false)

	done := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case <-done: // closed as expected
	case <-time.After(2 * time.Second):
		t.Fatal("metrics socket stayed open after view permission was revoked")
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
