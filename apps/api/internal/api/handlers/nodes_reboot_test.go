package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

const rebootTestPassword = "correct horse battery staple"

type rebootEnv struct {
	store  *store.Store
	h      *NodeHandlers
	router http.Handler
	node   *store.Node
	token  string
	calls  int
	// next is what the fake agent answers.
	nextStatus int
	nextErr    error
}

func newRebootEnv(t *testing.T) *rebootEnv {
	t.Helper()
	ctx := context.Background()
	s := authTestStore(t)
	hash, err := auth.HashPassword(rebootTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateUser(ctx, "admin@example.com", hash, "admin")
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateNode(ctx, &store.Node{Name: "host-1", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueAccessToken("secret", admin.ID, admin.Email, "admin")
	if err != nil {
		t.Fatal(err)
	}

	e := &rebootEnv{store: s, node: node, token: token, nextStatus: http.StatusAccepted}
	e.h = NewNodeHandlers(s, nil)
	e.h.rebootHost = func(_ context.Context, n *store.Node) (int, error) {
		e.calls++
		if n.ID != node.ID {
			t.Errorf("rebooted node %s, want %s", n.ID, node.ID)
		}
		return e.nextStatus, e.nextErr
	}
	r := chi.NewRouter()
	r.Use(auth.Middleware("secret", auth.NewTicketStore(), nil))
	r.Post("/api/v1/nodes/{id}/reboot", e.h.Reboot)
	e.router = r
	return e
}

func (e *rebootEnv) reboot(t *testing.T, nodeID, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/"+nodeID+"/reboot", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

func (e *rebootEnv) auditActions(t *testing.T) []string {
	t.Helper()
	entries, err := e.store.ListAudit(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range entries {
		if strings.HasPrefix(a.Action, "node.reboot") {
			out = append(out, a.Action)
		}
	}
	return out
}

// Without the password nothing reaches the agent, and the attempt is recorded.
func TestNodeRebootRequiresTheStepUp(t *testing.T) {
	e := newRebootEnv(t)
	for _, pw := range []string{"", "wrong"} {
		if rr := e.reboot(t, e.node.ID, pw); rr.Code != http.StatusUnauthorized {
			t.Fatalf("password %q: status=%d, want 401", pw, rr.Code)
		}
	}
	if e.calls != 0 {
		t.Fatalf("the agent was asked to reboot %d times without a valid step-up", e.calls)
	}
	if got := e.auditActions(t); len(got) != 2 || got[0] != "node.reboot.denied" {
		t.Fatalf("audit=%v, want two node.reboot.denied rows", got)
	}
}

func TestNodeRebootAcceptedByTheAgent(t *testing.T) {
	e := newRebootEnv(t)
	before := time.Now().UTC().Add(-time.Second)
	rr := e.reboot(t, e.node.ID, rebootTestPassword)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want 202", rr.Code, rr.Body.String())
	}
	// The dashboard tracks the reboot against the server's own clock.
	var accepted struct {
		Status      string    `json:"status"`
		RequestedAt time.Time `json:"requested_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "rebooting" || accepted.RequestedAt.Before(before) || accepted.RequestedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("response=%+v, want status rebooting and a current server time", accepted)
	}
	if e.calls != 1 {
		t.Fatalf("agent calls=%d, want 1", e.calls)
	}
	if got := e.auditActions(t); len(got) != 1 || got[0] != "node.reboot" {
		t.Fatalf("audit=%v, want one node.reboot row", got)
	}
}

func TestNodeRebootUnknownNode(t *testing.T) {
	e := newRebootEnv(t)
	if rr := e.reboot(t, "no-such-node", rebootTestPassword); rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rr.Code)
	}
	if e.calls != 0 {
		t.Fatal("an unknown node reached the agent")
	}
}

// The agent's refusals say what to change, so they reach the admin intact; an
// agent that predates the endpoint, or one that is unreachable, gets its own
// message.
func TestNodeRebootReportsAgentRefusals(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		err        error
		wantStatus int
		wantText   string
	}{
		{"disabled", http.StatusForbidden, errors.New("agent: host reboot is disabled on this agent (set AGENT_ALLOW_REBOOT=1 to enable it)"), http.StatusConflict, "AGENT_ALLOW_REBOOT"},
		{"not granted", http.StatusPreconditionFailed, errors.New("agent: the host does not allow this agent to reboot it; install the polkit rule"), http.StatusConflict, "polkit"},
		{"in progress", http.StatusConflict, errors.New("agent: a reboot is already in progress"), http.StatusConflict, "in progress"},
		{"old agent", http.StatusNotFound, errors.New("agent: HTTP 404"), http.StatusConflict, "too old"},
		{"unreachable", 0, errors.New("dial tcp: connection refused"), http.StatusBadGateway, "could not be reached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRebootEnv(t)
			e.nextStatus, e.nextErr = tc.status, tc.err
			rr := e.reboot(t, e.node.ID, rebootTestPassword)
			if rr.Code != tc.wantStatus || !strings.Contains(rr.Body.String(), tc.wantText) {
				t.Fatalf("status=%d body=%s, want %d containing %q", rr.Code, rr.Body.String(), tc.wantStatus, tc.wantText)
			}
			if got := e.auditActions(t); len(got) != 1 || got[0] != "node.reboot.failed" {
				t.Fatalf("audit=%v, want one node.reboot.failed row", got)
			}
		})
	}
}
