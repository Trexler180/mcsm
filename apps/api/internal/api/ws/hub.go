package ws

import (
	"context"
	"log"
	"net/http"

	"github.com/coder/websocket"
	"github.com/mcsm/api/internal/agent"
)

// ProxyConsole upgrades the browser connection and bidirectionally proxies
// to the agent's console WebSocket.
func ProxyConsole(w http.ResponseWriter, r *http.Request, agentClient *agent.Client, serverID string) {
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
			if err := agentConn.Write(ctx, msgType, data); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}

// ProxyMetrics proxies the agent metrics WebSocket to the browser.
func ProxyMetrics(w http.ResponseWriter, r *http.Request, agentClient *agent.Client, serverID string) {
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

	agentURL := agentClient.WebSocketURL("/agent/v1/servers/" + serverID + "/metrics")
	agentConn, _, err := websocket.Dial(ctx, agentURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentClient.Token}},
	})
	if err != nil {
		browserConn.Close(websocket.StatusInternalError, "agent connection failed")
		return
	}
	defer agentConn.CloseNow()

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
