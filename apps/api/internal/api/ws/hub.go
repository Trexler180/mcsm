package ws

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/mcsm/api/internal/agent"
)

type PermissionCheck func(context.Context) bool

// Re-check cadence for live permission revocation. Console is high-risk (can
// run server commands) so it polls faster than metrics. Package vars rather
// than constants so tests can drive revocation without real-time waits.
var (
	consoleRecheckInterval = 5 * time.Second
	metricsRecheckInterval = 30 * time.Second
)

// ProxyConsole upgrades the browser connection and bidirectionally proxies
// to the agent's console WebSocket.
func ProxyConsole(w http.ResponseWriter, r *http.Request, agentClient *agent.Client, serverID string, canUse PermissionCheck) {
	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		OriginPatterns:     []string{"*"},
	})
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

	agentURL := agentClient.WebSocketURL("/agent/v1/servers/" + serverID + "/console")
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
	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		OriginPatterns:     []string{"*"},
	})
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
