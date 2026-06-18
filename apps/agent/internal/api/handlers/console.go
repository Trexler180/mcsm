package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

type ConsoleHandlers struct {
	mgr        *process.Manager
	serverRoot string
}

func NewConsoleHandlers(mgr *process.Manager, serverRoot string) *ConsoleHandlers {
	return &ConsoleHandlers{mgr: mgr, serverRoot: serverRoot}
}

type wsMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (h *ConsoleHandlers) Console(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()

	ch, unsub, err := h.mgr.Subscribe(id)
	if err != nil {
		// server not running — still allow connection, send offline status
		if err := wsjson.Write(ctx, conn, map[string]any{
			"type": "status",
			"data": map[string]string{"status": "offline"},
		}); err != nil {
			return
		}
		conn.Close(websocket.StatusNormalClosure, "server offline")
		return
	}
	defer unsub()

	writeStatus := func(s process.Status) error {
		return wsjson.Write(ctx, conn, map[string]any{
			"type": "status",
			"data": map[string]string{"status": string(s)},
		})
	}

	// reader: commands from client
	go func() {
		for {
			var msg wsMsg
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
			if msg.Type == "input" {
				var d struct {
					Command string `json:"command"`
				}
				if err := json.Unmarshal(msg.Data, &d); err == nil && d.Command != "" {
					_ = h.mgr.SendCommand(id, d.Command)
				}
			}
		}
	}()

	// Tell the client the current status immediately, so a connection (or a
	// reconnect after a restart spawned a fresh instance) learns the server is
	// starting/online without waiting for a log line. The agent's console events
	// are log lines only, so a status change — most importantly the flip to
	// "online" once the server finishes booting — is surfaced by polling.
	lastStatus := h.mgr.Status(id).Status
	if err := writeStatus(lastStatus); err != nil {
		return
	}
	statusTicker := time.NewTicker(time.Second)
	defer statusTicker.Stop()
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// writer: events to client
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				// process exited
				_ = writeStatus(h.mgr.Status(id).Status)
				conn.Close(websocket.StatusNormalClosure, "server stopped")
				return
			}
			if err := wsjson.Write(ctx, conn, map[string]any{
				"type": "line",
				"data": event,
			}); err != nil {
				return
			}
		case <-statusTicker.C:
			if s := h.mgr.Status(id).Status; s != lastStatus {
				lastStatus = s
				if err := writeStatus(s); err != nil {
					return
				}
			}
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			// keepalive ping
			if err := conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}

func (h *ConsoleHandlers) RegisterDir(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Directory string `json:"directory"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Directory == "" {
		writeError(w, http.StatusBadRequest, "directory required")
		return
	}
	dir, err := validateServerDirectory(h.serverRoot, body.Directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.mgr.RegisterDir(id, dir)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
