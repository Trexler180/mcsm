package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Sink receives what a linked mod reports. It is the seam between the transport
// and the rest of the agent, so the process manager and metrics collector can
// consume mod data without knowing anything about WebSockets.
type Sink interface {
	// OnConnect fires when a mod completes its handshake.
	OnConnect(serverID string, hello Hello)
	// OnDisconnect fires exactly once per OnConnect.
	OnDisconnect(serverID string)
	// OnSnapshot delivers authoritative state. The agent must prefer this over
	// its own event-derived view.
	OnSnapshot(serverID string, snap Snapshot)
	// OnEvent delivers a best-effort notification. May be dropped in flight, so
	// never derive durable state from it alone.
	OnEvent(serverID string, ev Event)
}

// defaultRPCTimeout bounds a call from the agent side. The mod has its own
// shorter watchdog; this exists only for the case where the mod dies mid-call.
const defaultRPCTimeout = 15 * time.Second

// heartbeatInterval is handed to the mod in the welcome frame so snapshots line
// up with the agent's existing metric sampling cadence.
const heartbeatInterval = 15 * time.Second

// maxFrameBytes caps a single frame. Generous for a snapshot with a full player
// list, small enough that a malfunctioning mod cannot exhaust agent memory.
const maxFrameBytes = 256 * 1024

// idleTimeout evicts a session whose peer has gone silent. A healthy mod sends
// a snapshot every heartbeat, so several missed heartbeats mean the connection
// is dead or the mod is wedged — and holding the registry slot for it would
// make the mod's next reconnect bounce off CloseDuplicate until TCP notices,
// which on a half-open connection can be effectively never.
//
// A variable rather than a const only so tests can shrink it.
var idleTimeout = 4 * heartbeatInterval

// Session is one live mod connection.
type Session struct {
	serverID string
	conn     *websocket.Conn
	hello    Hello
	sink     Sink

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan RPCResponse
	nextID    uint64

	closeOnce sync.Once
	done      chan struct{}
}

func newSession(serverID string, conn *websocket.Conn, sink Sink) *Session {
	return &Session{
		serverID: serverID,
		conn:     conn,
		sink:     sink,
		pending:  make(map[string]chan RPCResponse),
		done:     make(chan struct{}),
	}
}

// Hello returns what the mod announced about itself.
func (s *Session) Hello() Hello {
	return s.hello
}

// ServerID identifies which server this session belongs to.
func (s *Session) ServerID() string {
	return s.serverID
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// serve runs the read loop until the connection ends or the peer goes silent
// for longer than idleTimeout. It expects the handshake to have already
// completed.
func (s *Session) serve(ctx context.Context) error {
	defer s.finish()

	for {
		typ, data, err := s.readWithDeadline(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			// The protocol is text-only; a binary frame means the peer is not
			// speaking our protocol.
			continue
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			// One malformed frame must not kill an otherwise healthy link.
			continue
		}
		if env.V != ProtocolVersion {
			continue
		}

		switch env.Type {
		case TypeSnapshot:
			var snap Snapshot
			if err := json.Unmarshal(env.Data, &snap); err == nil {
				s.sink.OnSnapshot(s.serverID, snap)
			}
		case TypeEvent:
			var ev Event
			if err := json.Unmarshal(env.Data, &ev); err == nil {
				s.sink.OnEvent(s.serverID, ev)
			}
		case TypeRPCResponse:
			s.deliverResponse(env)
		default:
			// Unknown types are ignored so a newer mod can ship ahead of the agent.
		}
	}
}

// readWithDeadline reads one frame, giving the peer at most idleTimeout to say
// anything at all. Distinguishes "the peer went quiet" from "the server is
// shutting down" so the log line points at the right culprit.
func (s *Session) readWithDeadline(ctx context.Context) (websocket.MessageType, []byte, error) {
	readCtx, cancel := context.WithTimeout(ctx, idleTimeout)
	defer cancel()

	typ, data, err := s.conn.Read(readCtx)
	if err != nil && readCtx.Err() != nil && ctx.Err() == nil {
		// Our deadline fired, not the caller's. Close the socket so a half-open
		// connection is torn down and the registry slot frees for a reconnect.
		_ = s.conn.Close(websocket.StatusGoingAway, "idle timeout")
		return typ, data, fmt.Errorf("link: no frame for %s, evicting idle session", idleTimeout)
	}
	return typ, data, err
}

// deliverResponse routes a reply to whoever is waiting on it.
func (s *Session) deliverResponse(env Envelope) {
	if env.ID == "" {
		return
	}
	var resp RPCResponse
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		return
	}

	s.pendingMu.Lock()
	ch, ok := s.pending[env.ID]
	delete(s.pending, env.ID)
	s.pendingMu.Unlock()

	if !ok {
		// A late reply after a timeout. Expected occasionally; nothing to do.
		return
	}
	// Buffered channel, so this never blocks the read loop even if the caller
	// has already walked away.
	ch <- resp
}

// Call invokes an RPC method on the mod and waits for its reply.
//
// A failed call returns an error; a call the mod refused returns a non-nil
// *RPCError in the response with OK false. Callers should distinguish: the first
// means the link is unhealthy, the second means the request was understood and
// declined.
//
// Errors returned before the request reached the socket wrap ErrNotDelivered.
// That distinction is not cosmetic: for a side-effecting method the caller can
// only safely retry down another path — stdin, say — when the mod provably never
// saw the request. A timeout or a session that died mid-flight carries no such
// guarantee, since the command may well have executed before the reply was lost,
// and those deliberately do not wrap the sentinel.
func (s *Session) Call(ctx context.Context, method string, params any) (RPCResponse, error) {
	select {
	case <-s.done:
		return RPCResponse{}, fmt.Errorf("%w: session closed", ErrNotDelivered)
	default:
	}

	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return RPCResponse{}, fmt.Errorf("%w: encode params: %v", ErrNotDelivered, err)
		}
		raw = encoded
	}

	s.pendingMu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", s.serverID, s.nextID)
	ch := make(chan RPCResponse, 1)
	s.pending[id] = ch
	s.pendingMu.Unlock()

	cleanup := func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}

	frame, err := json.Marshal(Envelope{
		V:    ProtocolVersion,
		Type: TypeRPCRequest,
		ID:   id,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(RPCRequest{Method: method, Params: raw}),
	})
	if err != nil {
		cleanup()
		return RPCResponse{}, fmt.Errorf("%w: encode request: %v", ErrNotDelivered, err)
	}

	if err := s.write(ctx, frame); err != nil {
		cleanup()
		// A write that errors did not put a complete frame on the wire, and a
		// partial one cannot parse as an envelope, so the mod cannot have acted on
		// it either way.
		return RPCResponse{}, fmt.Errorf("%w: %v", ErrNotDelivered, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	select {
	case resp := <-ch:
		return resp, nil
	case <-callCtx.Done():
		// Deliberately not ErrNotDelivered: the request is already on the wire, so
		// the mod may have executed it and merely failed to answer in time.
		cleanup()
		return RPCResponse{}, fmt.Errorf("link: %s timed out", method)
	case <-s.done:
		cleanup()
		return RPCResponse{}, errors.New("link: session closed while waiting")
	}
}

// write serialises access to the connection: coder/websocket permits only one
// writer at a time.
func (s *Session) write(ctx context.Context, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("link: write: %w", err)
	}
	return nil
}

// close ends the session with a protocol close code.
func (s *Session) close(code websocket.StatusCode, reason string) {
	_ = s.conn.Close(code, reason)
	s.finish()
}

// finish releases everyone waiting on this session exactly once.
func (s *Session) finish() {
	s.closeOnce.Do(func() {
		close(s.done)

		s.pendingMu.Lock()
		pending := s.pending
		s.pending = make(map[string]chan RPCResponse)
		s.pendingMu.Unlock()

		// Callers blocked on a reply that will now never arrive fall through to
		// their session-closed case; nothing to send, just stop holding them.
		_ = pending
	})
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		// The types here are fixed and known-encodable; a failure is a bug, not
		// a runtime condition worth propagating through every call site.
		return json.RawMessage(`{}`)
	}
	return data
}
