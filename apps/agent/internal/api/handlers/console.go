package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// Console streams a server's console and accepts commands for it.
//
// Frames sent: {"type":"hello","data":{"run_id","resumed","status"}} when a
// run's stream begins, then {"type":"line","data":{line,stream,ts,seq}} and
// {"type":"status","data":{"status"}}. A client that reconnects with
// ?run=<run_id>&since=<seq> receives only what it missed (resumed=true); a
// hello with resumed=false means "start over": clear what you show, the run's
// history follows. The socket stays open while the server is stopped, and the
// next run's hello arrives when it starts.
func (h *ConsoleHandlers) Console(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	runID := r.URL.Query().Get("run")
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)

	conn, err := acceptAPISocket(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	write := func(typ string, data any) error {
		return wsjson.Write(ctx, conn, map[string]any{"type": typ, "data": data})
	}

	// Commands are executed on their own goroutine rather than inline in the
	// reader. Writing to stdin was effectively instant, but ExecCommand may wait
	// on an RPC reply from the helper mod, and doing that inline would let one
	// wedged mod freeze console input for the length of its timeout.
	//
	// A single worker, not one goroutine per command: the operator's commands
	// must execute in the order they were typed, and fanning out would let a slow
	// command be overtaken by the next one. The queue is bounded, so a mod that
	// has stopped answering eventually applies backpressure to the reader
	// instead of growing without limit.
	commands := make(chan string, 32)
	go func() {
		for cmd := range commands {
			// The reply is discarded rather than echoed back: whatever the command
			// prints reaches this console anyway through the log tail, and
			// injecting the RPC's captured output too would show every line twice.
			_, _ = h.mgr.ExecCommand(ctx, id, cmd)
		}
	}()
	go func() {
		defer close(commands)
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
					select {
					case commands <- d.Command:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	pollTicker := time.NewTicker(time.Second)
	defer pollTicker.Stop()

	reportedOffline := false
	for {
		stream, err := h.mgr.SubscribeConsole(id, runID, since)
		if err != nil {
			// Not running: say so once, then wait for a start.
			if !reportedOffline {
				if err := write("status", map[string]string{"status": string(process.StatusOffline)}); err != nil {
					return
				}
				reportedOffline = true
			}
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				if err := conn.Ping(ctx); err != nil {
					return
				}
			case <-pollTicker.C:
			}
			continue
		}
		reportedOffline = false
		if !h.streamRun(ctx, id, stream, write, conn, pingTicker, pollTicker) {
			stream.Cancel()
			return
		}
		stream.Cancel()
		// The run ended. Whatever starts next is a new run, streamed from its
		// beginning.
		runID, since = "", 0
	}
}

// streamRun forwards one run's console until it ends (true) or the client goes
// away (false).
func (h *ConsoleHandlers) streamRun(ctx context.Context, id string, stream *process.ConsoleStream,
	write func(string, any) error, conn *websocket.Conn, pingTicker, pollTicker *time.Ticker) bool {
	lastStatus := h.mgr.Status(id).Status
	if err := write("hello", map[string]any{
		"run_id": stream.RunID, "resumed": stream.Resumed, "status": string(lastStatus),
	}); err != nil {
		return false
	}
	// The status as well, for clients that predate the hello frame. The
	// agent's console events are log lines only, so a status change — most
	// importantly the flip to "online" once the server finishes booting — is
	// surfaced by polling.
	if err := write("status", map[string]string{"status": string(lastStatus)}); err != nil {
		return false
	}
	for {
		select {
		case event, ok := <-stream.Events:
			if !ok {
				return write("status", map[string]string{"status": string(h.mgr.Status(id).Status)}) == nil
			}
			if err := write("line", event); err != nil {
				return false
			}
		case <-pollTicker.C:
			if s := h.mgr.Status(id).Status; s != lastStatus {
				lastStatus = s
				if err := write("status", map[string]string{"status": string(s)}); err != nil {
					return false
				}
			}
		case <-pingTicker.C:
			if err := conn.Ping(ctx); err != nil {
				return false
			}
		case <-ctx.Done():
			return false
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

// acceptAPISocket accepts a WebSocket from the panel's API. Origin checking
// protects browsers from cross-site sockets; nothing but the API connects to
// the agent, every request already carries the agent token (checked by the
// auth middleware before this runs), and the API sends no browser Origin
// worth checking — so verification is off on purpose, here and only here.
func acceptAPISocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
}
