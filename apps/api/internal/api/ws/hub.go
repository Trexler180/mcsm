package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/mcsm/api/internal/agent"
)

type PermissionCheck func(context.Context) bool

// allowedOrigins pins the set of browser Origins permitted to open a console /
// metrics WebSocket. When empty (the default), any origin is accepted — needed
// for split-origin local dev — and the connection relies solely on its
// single-use ticket. When set (production, via APP_ORIGIN), the library enforces
// the Origin against this list as defense in depth against cross-site hijacking.
var (
	originMu       sync.RWMutex
	allowedOrigins []string
)

// SetAllowedOrigins configures the WebSocket Origin allowlist process-wide.
func SetAllowedOrigins(origins []string) {
	originMu.Lock()
	defer originMu.Unlock()
	allowedOrigins = append([]string(nil), origins...)
}

// AcceptOptions is acceptOptions for handlers outside this package that
// upgrade their own connections.
func AcceptOptions() *websocket.AcceptOptions { return acceptOptions() }

// acceptOptions builds AcceptOptions honoring the configured allowlist. With no
// allowlist it skips Origin verification (ticket-only auth); with one it enforces
// the patterns instead of blanket-skipping.
func acceptOptions() *websocket.AcceptOptions {
	originMu.RLock()
	defer originMu.RUnlock()
	if len(allowedOrigins) == 0 {
		return &websocket.AcceptOptions{InsecureSkipVerify: true}
	}
	return &websocket.AcceptOptions{OriginPatterns: append([]string(nil), allowedOrigins...)}
}

// Re-check cadence for live permission revocation. Console is high-risk (can
// run server commands) so it polls faster than metrics. Package vars rather
// than constants so tests can drive revocation without real-time waits.
var (
	consoleRecheckInterval = 5 * time.Second
	metricsRecheckInterval = 30 * time.Second
)

// ProxyConsole upgrades the browser connection and bidirectionally proxies
// to the agent's console WebSocket.
// ConsoleInput is called with each command typed into a console socket,
// before it is forwarded, so it can be recorded.
type ConsoleInput func(ctx context.Context, command string)

// maxConsoleCommand matches the limit on POST /command.
const maxConsoleCommand = 1000

// consoleCommand extracts the command from a browser console frame, which is
// {"type":"input","data":{"command":"..."}}. ok is false for anything else;
// forward reports whether the frame may go to the agent at all. A command with
// a line break would run as several commands but be recorded as one, so it is
// refused rather than forwarded.
func consoleCommand(msgType websocket.MessageType, data []byte) (cmd string, isInput, forward bool) {
	if msgType != websocket.MessageText {
		return "", false, true
	}
	var frame struct {
		Type string `json:"type"`
		Data struct {
			Command string `json:"command"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Type != "input" {
		return "", false, true
	}
	cmd = frame.Data.Command
	if cmd == "" || len(cmd) > maxConsoleCommand || strings.ContainsAny(cmd, "\r\n") {
		return cmd, true, false
	}
	return cmd, true, true
}

func ProxyConsole(w http.ResponseWriter, r *http.Request, agentClient *agent.Client, serverID string, canUse PermissionCheck, onInput ConsoleInput) {
	browserConn, err := websocket.Accept(w, r, acceptOptions())
	if err != nil {
		return
	}
	defer browserConn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if canUse != nil && !canUse(ctx) {
		browserConn.Close(websocket.StatusPolicyViolation, "permission revoked")
		return
	}

	agentURL := agentClient.WebSocketURL("/agent/v1/servers/"+serverID+"/console") + resumeQuery(r)
	agentConn, _, err := websocket.Dial(ctx, agentURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentClient.Token}},
	})
	if err != nil {
		log.Printf("ws: agent console dial failed for %s: %v", serverID, err)
		browserConn.Close(websocket.StatusInternalError, "agent connection failed")
		return
	}
	defer agentConn.CloseNow()

	errc := make(chan error, 2)
	go closeWhenPermissionRevoked(ctx, cancel, browserConn, canUse, consoleRecheckInterval)

	go func() {
		for {
			msgType, data, err := agentConn.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if err := browserConn.Write(ctx, msgType, data); err != nil {
				errc <- err
				return
			}
		}
	}()

	go func() {
		for {
			msgType, data, err := browserConn.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if canUse != nil && !canUse(ctx) {
				errc <- errors.New("permission revoked")
				return
			}
			cmd, isInput, forward := consoleCommand(msgType, data)
			if !forward {
				continue
			}
			if isInput && onInput != nil {
				onInput(ctx, cmd)
			}
			if err := agentConn.Write(ctx, msgType, data); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}

// ProxyMetrics proxies the agent metrics WebSocket to the browser.
func ProxyMetrics(w http.ResponseWriter, r *http.Request, agentClient *agent.Client, serverID string, canUse PermissionCheck) {
	browserConn, err := websocket.Accept(w, r, acceptOptions())
	if err != nil {
		return
	}
	defer browserConn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if canUse != nil && !canUse(ctx) {
		browserConn.Close(websocket.StatusPolicyViolation, "permission revoked")
		return
	}

	agentURL := agentClient.WebSocketURL("/agent/v1/servers/" + serverID + "/metrics")
	agentConn, _, err := websocket.Dial(ctx, agentURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentClient.Token}},
	})
	if err != nil {
		browserConn.Close(websocket.StatusInternalError, "agent connection failed")
		return
	}
	defer agentConn.CloseNow()

	go closeWhenPermissionRevoked(ctx, cancel, browserConn, canUse, metricsRecheckInterval)

	for {
		_, data, err := agentConn.Read(ctx)
		if err != nil {
			return
		}
		if err := browserConn.Write(ctx, websocket.MessageText, data); err != nil {
			return
		}
	}
}

func closeWhenPermissionRevoked(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, canUse PermissionCheck, interval time.Duration) {
	if canUse == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !canUse(ctx) {
				conn.Close(websocket.StatusPolicyViolation, "permission revoked")
				cancel()
				return
			}
		}
	}
}

var runIDRe = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// resumeQuery passes a reconnecting console's position on to the agent: the
// run it was watching and the last line it has. Only those two, validated; the
// browser's ticket stays here.
func resumeQuery(r *http.Request) string {
	q := r.URL.Query()
	run, since := q.Get("run"), q.Get("since")
	n, err := strconv.ParseInt(since, 10, 64)
	if !runIDRe.MatchString(run) || err != nil || n < 1 {
		return ""
	}
	return "?" + url.Values{"run": {run}, "since": {strconv.FormatInt(n, 10)}}.Encode()
}
