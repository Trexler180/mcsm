package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/store"
)

// EventHandlers serve the live event stream.
type EventHandlers struct {
	store *store.Store
	bus   *events.Bus
}

func NewEventHandlers(s *store.Store, bus *events.Bus) *EventHandlers {
	return &EventHandlers{store: s, bus: bus}
}

// visibilityRefresh is how often a stream re-reads what its user may see, so a
// revoked grant or a demotion stops the flow without a reconnect.
var visibilityRefresh = 30 * time.Second

// scope is what one subscriber may see.
type scope struct {
	mu      sync.RWMutex
	admin   bool
	servers map[string]bool
	nodes   map[string]bool
}

func (sc *scope) allows(e events.Event, userID string) bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	if e.UserID != "" {
		return e.UserID == userID
	}
	if e.AdminOnly {
		return sc.admin
	}
	if sc.admin {
		return true
	}
	if e.ServerID != "" {
		return sc.servers[e.ServerID]
	}
	if e.NodeID != "" {
		return sc.nodes[e.NodeID]
	}
	return false
}

// load reads the user's visible servers and the nodes hosting them. It
// reports false when the account or its session is gone.
func (h *EventHandlers) load(ctx context.Context, sc *scope, claims *auth.Claims) bool {
	user, err := h.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		return false
	}
	if claims.SessionID != "" {
		if ok, err := h.store.SessionActive(ctx, claims.SessionID, claims.UserID); err != nil || !ok {
			return false
		}
	}
	var servers []*store.Server
	if user.Role == "admin" {
		servers, err = h.store.ListServers(ctx)
	} else {
		servers, err = h.store.ListServersForUser(ctx, user.ID)
	}
	if err != nil {
		return false
	}
	ids, nodes := map[string]bool{}, map[string]bool{}
	for _, s := range servers {
		ids[s.ID] = true
		nodes[s.NodeID] = true
	}
	sc.mu.Lock()
	sc.admin, sc.servers, sc.nodes = user.Role == "admin", ids, nodes
	sc.mu.Unlock()
	return true
}

// Stream is the dashboard's live feed: one WebSocket carrying every event the
// user may see — server status and stats, operation progress, node health,
// their own alerts. ?since=<seq> resumes after a dropped connection; when the
// gap is too old to replay, the first message is stream.reset and the client
// refetches what it shows.
func (h *EventHandlers) Stream(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	sc := &scope{}
	if !h.load(r.Context(), sc, claims) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	conn, err := websocket.Accept(w, r, ws.AcceptOptions())
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub, backlog, resumed := h.bus.Subscribe(since)
	defer sub.Close()

	write := func(v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return true
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, data) == nil
	}

	hello := map[string]any{"type": "stream.hello", "seq": h.bus.Seq(), "resumed": resumed}
	if !resumed {
		hello["type"] = events.StreamReset
	}
	if !write(hello) {
		return
	}
	for _, e := range backlog {
		if sc.allows(e, claims.UserID) && !write(e) {
			return
		}
	}

	// The client sends nothing; reading notices it going away.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	refresh := time.NewTicker(visibilityRefresh)
	defer refresh.Stop()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			if !h.load(ctx, sc, claims) {
				conn.Close(websocket.StatusPolicyViolation, "session ended")
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case e, ok := <-sub.C():
			if !ok {
				// Fell too far behind: tell the client to refetch and resume.
				_ = write(map[string]any{"type": events.StreamReset, "seq": h.bus.Seq()})
				conn.Close(websocket.StatusTryAgainLater, "lagging")
				return
			}
			if sc.allows(e, claims.UserID) && !write(e) {
				return
			}
		}
	}
}
