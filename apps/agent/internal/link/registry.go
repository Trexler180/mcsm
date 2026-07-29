package link

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// TokenValidator checks a per-launch token against the server it claims to be.
//
// Tokens are minted when the agent spawns a server and are persisted in run
// state, so a server that outlived an agent restart can still re-authenticate
// when it reconnects — the reattach case that makes this more than a map lookup.
type TokenValidator interface {
	ValidateLaunchToken(serverID, token string) bool
}

// Registry owns every live mod session on this host.
type Registry struct {
	sink   Sink
	tokens TokenValidator

	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewRegistry builds a registry. Sink may not be nil.
func NewRegistry(sink Sink, tokens TokenValidator) *Registry {
	return &Registry{
		sink:     sink,
		tokens:   tokens,
		sessions: make(map[string]*Session),
	}
}

// Get returns the live session for a server, if the mod is connected.
//
// Callers use the boolean to decide between the mod path and the legacy
// scrape/stdin fallback — this is the single point where "is the mod available"
// is answered.
func (r *Registry) Get(serverID string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[serverID]
	return s, ok
}

// Connected reports whether a server currently has a helper mod linked.
func (r *Registry) Connected(serverID string) bool {
	_, ok := r.Get(serverID)
	return ok
}

// Handle serves the mod link endpoint. Mount it at /agent/v1/link/{id}.
//
// Authentication happens before the WebSocket upgrade, so a bad token never
// results in a live session.
func (r *Registry) Handle(w http.ResponseWriter, req *http.Request) {
	serverID := chi.URLParam(req, "id")
	if serverID == "" {
		http.Error(w, "missing server id", http.StatusBadRequest)
		return
	}

	token := bearerToken(req.Header.Get("Authorization"))
	if token == "" || r.tokens == nil || !r.tokens.ValidateLaunchToken(serverID, token) {
		// Deliberately terse: an attacker probing this endpoint learns nothing
		// about whether the server id exists.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxFrameBytes)

	// The handshake must not hang a goroutine forever on a peer that connects
	// and then says nothing.
	ctx := req.Context()
	handshakeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hello, err := readHello(handshakeCtx, conn)
	if err != nil {
		_ = conn.Close(CloseBadVersion, "expected hello")
		return
	}

	session := newSession(serverID, conn, r.sink)
	session.hello = hello

	if !r.register(session) {
		// A second live session for one server is a bug somewhere, not a race to
		// resolve by preferring the newcomer: the existing session may be
		// mid-RPC.
		_ = conn.Close(CloseDuplicate, "session already established")
		return
	}
	defer r.unregister(serverID, session)

	if err := sendWelcome(ctx, session); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "welcome failed")
		return
	}

	r.sink.OnConnect(serverID, hello)
	defer r.sink.OnDisconnect(serverID)

	log.Printf("helper mod linked: server=%s mc=%s loader=%s/%s mod=%s",
		serverID, hello.MCVersion, hello.Loader, hello.LoaderVersion, hello.ModVersion)

	if err := session.serve(ctx); err != nil {
		log.Printf("helper mod link closed: server=%s: %v", serverID, err)
	}
}

// register adds a session unless one already exists for that server.
func (r *Registry) register(s *Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[s.serverID]; exists {
		return false
	}
	r.sessions[s.serverID] = s
	return true
}

// unregister removes a session only if it is still the current one, so a
// reconnect that already replaced it is not clobbered by the old session's
// deferred cleanup.
func (r *Registry) unregister(serverID string, s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.sessions[serverID]; ok && current == s {
		delete(r.sessions, serverID)
	}
	s.finish()
}

// readHello waits for the opening frame and validates it.
func readHello(ctx context.Context, conn *websocket.Conn) (Hello, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return Hello{}, err
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Hello{}, err
	}
	if env.V != ProtocolVersion || env.Type != TypeHello {
		return Hello{}, errBadHandshake
	}

	var hello Hello
	if err := json.Unmarshal(env.Data, &hello); err != nil {
		return Hello{}, err
	}
	return hello, nil
}

// sendWelcome accepts the session and dictates the heartbeat cadence, which is
// aligned to the agent's existing metric sampling so one snapshot maps to one
// metric row.
func sendWelcome(ctx context.Context, s *Session) error {
	welcome := Welcome{
		HeartbeatMS:          heartbeatInterval.Milliseconds(),
		AcceptedCapabilities: s.hello.Capabilities,
		MaxFrameBytes:        maxFrameBytes,
	}

	frame, err := json.Marshal(Envelope{
		V:    ProtocolVersion,
		Type: TypeWelcome,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(welcome),
	})
	if err != nil {
		return err
	}
	return s.write(ctx, frame)
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

type handshakeError struct{}

func (handshakeError) Error() string { return "link: bad handshake" }

var errBadHandshake = handshakeError{}
