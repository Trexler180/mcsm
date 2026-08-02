package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ExecCommand runs a console command through a server's helper mod and returns
// what the command actually replied — the thing writing to stdin can never tell
// us.
//
// The error contract is the important part, because the caller's fallback is to
// run the same command again over stdin. ErrNoSession and ErrNotDelivered both
// mean the mod provably never received the request, so retrying is safe. Every
// other error — a timeout above all — means the command may already have run,
// and re-issuing it would execute it twice. Callers must not fall back on those.
//
// A command the server understood and rejected is not an error here: it comes
// back as ExecResult.Success false, which is a fact about the command rather
// than about the link.
func (r *Registry) ExecCommand(ctx context.Context, serverID, cmd string) (ExecResult, error) {
	session, ok := r.Get(serverID)
	if !ok {
		return ExecResult{}, ErrNoSession
	}

	resp, err := session.Call(ctx, MethodCommandExec, map[string]string{"command": cmd})
	if err != nil {
		return ExecResult{}, err
	}
	if !resp.OK {
		// The mod refused the request outright (unknown method, bad params). It
		// did not execute anything, so this is safe to retry — an older mod that
		// predates command.exec must not strand the console.
		msg := "mod rejected command"
		if resp.Error != nil {
			msg = resp.Error.Code + ": " + resp.Error.Message
		}
		return ExecResult{}, fmt.Errorf("%w: %s", ErrNotDelivered, msg)
	}

	var result ExecResult
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			// The command ran; only our reading of the reply failed. Not retryable.
			return ExecResult{}, fmt.Errorf("link: decode exec result: %w", err)
		}
	}
	return result, nil
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
	// Every early return below closes the connection explicitly, but the ordinary
	// path — serve() returning after a read error — did not, leaving it to the
	// library to notice. CloseNow is idempotent and cheap, so make release
	// unconditional rather than dependent on which way the session ended.
	defer conn.CloseNow()
	conn.SetReadLimit(maxFrameBytes)

	// The handshake must not hang a goroutine forever on a peer that connects
	// and then says nothing.
	ctx := req.Context()
	handshakeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hello, err := readHello(handshakeCtx, conn)
	if err != nil {
		// 4400 means "this agent will never speak your protocol" and tells the
		// mod to stop retrying forever, so it is reserved for an actual version
		// mismatch. Every other handshake failure — a timeout, a malformed frame,
		// a network hiccup — is transient, and closing it with 4400 would
		// permanently disable a mod that a simple retry would have connected.
		if errors.Is(err, errBadVersion) {
			_ = conn.Close(CloseBadVersion, "unsupported protocol version")
		} else {
			_ = conn.Close(websocket.StatusProtocolError, "expected hello")
		}
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

	if err := sendWelcome(ctx, session); err != nil {
		r.unregister(serverID, session)
		_ = conn.Close(websocket.StatusInternalError, "welcome failed")
		return
	}

	r.sink.OnConnect(serverID, hello)
	defer func() {
		// Order matters, and defers run last-registered-first, so these are one
		// closure rather than two statements: the registry must already report
		// the server unlinked before the sink tells the rest of the agent to stop
		// trusting mod data. Reversed, a consumer reacting to OnDisconnect would
		// still find a live session in the registry and could re-derive from it.
		r.unregister(serverID, session)
		r.sink.OnDisconnect(serverID)
	}()

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
	if env.Type != TypeHello {
		return Hello{}, errBadHandshake
	}
	if env.V != ProtocolVersion {
		return Hello{}, errBadVersion
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
		AcceptedCapabilities: knownCapabilities(s.hello.Capabilities),
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

// knownCapabilities filters what the mod announced down to what this agent
// actually understands, so accepted_capabilities is a real negotiation result
// rather than an echo — a newer mod learns which of its features this agent
// will use, instead of being told "all of them" by an agent that won't.
func knownCapabilities(announced []string) []string {
	known := map[string]bool{
		CapVitals:       true,
		CapPlayerEvents: true,
		CapRPC:          true,
		CapCommandExec:  true,
	}
	accepted := make([]string, 0, len(announced))
	for _, c := range announced {
		if known[c] {
			accepted = append(accepted, c)
		}
	}
	return accepted
}

var (
	errBadHandshake = errors.New("link: bad handshake")
	// errBadVersion is the one handshake failure the mod must not retry.
	errBadVersion = errors.New("link: unsupported protocol version")

	// ErrNoSession means the server has no helper mod linked at all.
	ErrNoSession = errors.New("link: no session")

	// ErrNotDelivered means an RPC request never reached the mod. It is the
	// caller's licence to retry a side-effecting call down a different path;
	// absence of it means the call may have taken effect, so a retry could
	// duplicate it.
	ErrNotDelivered = errors.New("link: request not delivered")
)
