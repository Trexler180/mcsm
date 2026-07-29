package link

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// These tests drive the real registry over a real loopback WebSocket with a
// fake mod on the other end. That covers the handshake, auth, RPC correlation
// and duplicate handling end to end without needing Minecraft, which is the
// cheapest honest proof available for this layer.

type fakeTokens struct {
	serverID string
	token    string
}

func (f fakeTokens) ValidateLaunchToken(serverID, token string) bool {
	return serverID == f.serverID && token == f.token
}

// recordingSink captures callbacks so assertions can wait on them.
type recordingSink struct {
	mu        sync.Mutex
	connected []string
	snapshots []Snapshot
	events    []Event
	dropped   int

	snapshotCh chan struct{}
	eventCh    chan struct{}
	connectCh  chan struct{}
}

func newRecordingSink() *recordingSink {
	return &recordingSink{
		snapshotCh: make(chan struct{}, 8),
		eventCh:    make(chan struct{}, 8),
		connectCh:  make(chan struct{}, 8),
	}
}

func (s *recordingSink) OnConnect(serverID string, hello Hello) {
	s.mu.Lock()
	s.connected = append(s.connected, serverID)
	s.mu.Unlock()
	select {
	case s.connectCh <- struct{}{}:
	default:
	}
}

func (s *recordingSink) OnDisconnect(string) {}

func (s *recordingSink) OnSnapshot(_ string, snap Snapshot) {
	s.mu.Lock()
	s.snapshots = append(s.snapshots, snap)
	s.mu.Unlock()
	select {
	case s.snapshotCh <- struct{}{}:
	default:
	}
}

func (s *recordingSink) OnEvent(_ string, ev Event) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	select {
	case s.eventCh <- struct{}{}:
	default:
	}
}

// harness wires a registry behind an httptest server.
type harness struct {
	server   *httptest.Server
	registry *Registry
	sink     *recordingSink
	wsURL    string
}

func newHarness(t *testing.T, serverID, token string) *harness {
	t.Helper()

	sink := newRecordingSink()
	registry := NewRegistry(sink, fakeTokens{serverID: serverID, token: token})

	router := chi.NewRouter()
	router.Get("/agent/v1/link/{id}", registry.Handle)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &harness{
		server:   srv,
		registry: registry,
		sink:     sink,
		wsURL:    "ws" + strings.TrimPrefix(srv.URL, "http"),
	}
}

// dial connects as a mod would, presenting a bearer token.
func (h *harness) dial(t *testing.T, ctx context.Context, serverID, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	return websocket.Dial(ctx, h.wsURL+"/agent/v1/link/"+serverID, &websocket.DialOptions{
		HTTPHeader: header,
	})
}

// sendHello performs the mod side of the handshake and waits for welcome.
func sendHello(t *testing.T, ctx context.Context, conn *websocket.Conn) Welcome {
	t.Helper()

	hello := Hello{
		ModVersion:    "1.0.0",
		MCVersion:     "26.2",
		Loader:        "fabric",
		LoaderVersion: "0.19.3",
		ServerBrand:   "fabric",
		Capabilities:  []string{CapVitals, CapPlayerEvents, CapRPC, CapCommandExec},
	}
	writeFrame(t, ctx, conn, Envelope{
		V:    ProtocolVersion,
		Type: TypeHello,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(hello),
	})

	env := readFrame(t, ctx, conn)
	if env.Type != TypeWelcome {
		t.Fatalf("expected welcome, got %q", env.Type)
	}

	var welcome Welcome
	if err := json.Unmarshal(env.Data, &welcome); err != nil {
		t.Fatalf("decode welcome: %v", err)
	}
	return welcome
}

func writeFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, env Envelope) {
	t.Helper()
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func readFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) Envelope {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return env
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHandshakeAndSnapshot(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	conn, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	welcome := sendHello(t, ctx, conn)
	if welcome.HeartbeatMS <= 0 {
		t.Errorf("welcome carried heartbeat %d, want a positive interval", welcome.HeartbeatMS)
	}
	if welcome.MaxFrameBytes <= 0 {
		t.Errorf("welcome carried max frame %d, want a positive cap", welcome.MaxFrameBytes)
	}

	select {
	case <-h.sink.connectCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never saw the connection")
	}

	if !h.registry.Connected("srv1") {
		t.Error("registry does not report the server as connected after handshake")
	}

	snap := Snapshot{
		UptimeMS:   1000,
		TPS:        TPS{M1: 19.9, M5: 20, M15: 20},
		Dimensions: []Dimension{},
		Players: Players{
			Online: 1,
			Max:    20,
			List:   []PlayerInfo{{UUID: "u1", Name: "Steve", PingMS: 40, Dimension: "minecraft:overworld"}},
		},
	}
	writeFrame(t, ctx, conn, Envelope{
		V:    ProtocolVersion,
		Type: TypeSnapshot,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(snap),
	})

	select {
	case <-h.sink.snapshotCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never received the snapshot")
	}

	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	if len(h.sink.snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(h.sink.snapshots))
	}
	if got := h.sink.snapshots[0].Players.List[0].Name; got != "Steve" {
		t.Errorf("player name round-tripped as %q, want Steve", got)
	}
}

func TestRejectsBadToken(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	_, resp, err := h.dial(t, ctx, "srv1", "wrong-token")
	if err == nil {
		t.Fatal("dial with a bad token succeeded; it must be rejected before the upgrade")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
	if h.registry.Connected("srv1") {
		t.Error("a rejected connection registered a session")
	}
}

func TestRejectsMissingToken(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	if _, _, err := h.dial(t, ctx, "srv1", ""); err == nil {
		t.Fatal("dial without a token succeeded; it must be rejected")
	}
}

func TestRejectsDuplicateSession(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	first, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close(websocket.StatusNormalClosure, "")
	sendHello(t, ctx, first)

	second, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	defer second.Close(websocket.StatusNormalClosure, "")

	// The duplicate handshakes fine at HTTP level but must be closed rather than
	// allowed to displace a session that may be mid-RPC.
	writeFrame(t, ctx, second, Envelope{
		V:    ProtocolVersion,
		Type: TypeHello,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(Hello{ModVersion: "1.0.0"}),
	})

	_, _, readErr := second.Read(ctx)
	if readErr == nil {
		t.Fatal("duplicate session was not closed")
	}
	if status := websocket.CloseStatus(readErr); status != CloseDuplicate {
		t.Errorf("close status %d, want %d", status, CloseDuplicate)
	}
}

func TestRPCRoundTrip(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	conn, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	sendHello(t, ctx, conn)

	select {
	case <-h.sink.connectCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never saw the connection")
	}

	session, ok := h.registry.Get("srv1")
	if !ok {
		t.Fatal("session not registered")
	}

	// Fake mod: answer the next request with a captured-output result.
	go func() {
		env := readFrame(t, ctx, conn)
		if env.Type != TypeRPCRequest {
			return
		}
		writeFrame(t, ctx, conn, Envelope{
			V:    ProtocolVersion,
			Type: TypeRPCResponse,
			ID:   env.ID,
			TS:   time.Now().UnixMilli(),
			Data: mustMarshal(RPCResponse{
				OK:     true,
				Result: mustMarshal(ExecResult{Output: []string{"There are 1 of a max of 20 players online"}, Success: true}),
			}),
		})
	}()

	resp, err := session.Call(ctx, MethodCommandExec, map[string]string{"command": "list"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("call reported failure: %+v", resp.Error)
	}

	var result ExecResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Success || len(result.Output) != 1 {
		t.Errorf("unexpected exec result: %+v", result)
	}
}

// TestCallOnClosedSessionFails covers the case that matters operationally: the
// server died mid-call. The caller must get an error rather than block until a
// timeout that is longer than any UI is willing to wait.
func TestCallOnClosedSessionFails(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	conn, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendHello(t, ctx, conn)

	select {
	case <-h.sink.connectCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never saw the connection")
	}

	session, ok := h.registry.Get("srv1")
	if !ok {
		t.Fatal("session not registered")
	}

	_ = conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not observe the close")
	}

	if _, err := session.Call(ctx, MethodServerSave, nil); err == nil {
		t.Error("call on a closed session succeeded; it must fail fast")
	}
}

func TestEventDelivery(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	conn, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	sendHello(t, ctx, conn)

	writeFrame(t, ctx, conn, Envelope{
		V:    ProtocolVersion,
		Type: TypeEvent,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(Event{Kind: EventPlayerJoin, UUID: "u1", Name: "Steve"}),
	})

	select {
	case <-h.sink.eventCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never received the event")
	}

	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	if h.sink.events[0].Kind != EventPlayerJoin {
		t.Errorf("event kind %q, want %q", h.sink.events[0].Kind, EventPlayerJoin)
	}
}

// TestIgnoresGarbageFrames pins the rule from PROTOCOL.md §2: one malformed or
// unknown frame must not take down an otherwise healthy link, because that is
// how a newer mod stays compatible with an older agent.
func TestIgnoresGarbageFrames(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)

	conn, _, err := h.dial(t, ctx, "srv1", "secret-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	sendHello(t, ctx, conn)

	if err := conn.Write(ctx, websocket.MessageText, []byte("{not json")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	writeFrame(t, ctx, conn, Envelope{
		V:    ProtocolVersion,
		Type: "some_future_frame_type",
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(map[string]string{"a": "b"}),
	})

	// The link must still be usable afterwards.
	writeFrame(t, ctx, conn, Envelope{
		V:    ProtocolVersion,
		Type: TypeEvent,
		TS:   time.Now().UnixMilli(),
		Data: mustMarshal(Event{Kind: EventServerReady}),
	})

	select {
	case <-h.sink.eventCh:
	case <-time.After(5 * time.Second):
		t.Fatal("link stopped working after a malformed frame")
	}
}
