package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type NodeHandlers struct {
	store *store.Store
	// Rebooting a host is a step-up action, so it spends from the same
	// password-guess budget as login and every other step-up.
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
	// rebootHost asks a node's agent to reboot its machine. A field so tests
	// can stand in for the agent.
	rebootHost func(ctx context.Context, node *store.Node) (int, error)
}

// NewNodeHandlers builds the node handlers. pw is the password-guess budget
// shared with login and the other step-ups; nil gives a private one.
func NewNodeHandlers(s *store.Store, pw *PasswordThrottles) *NodeHandlers {
	pw = pw.orNew()
	return &NodeHandlers{
		store:        s,
		ipThrottle:   pw.IP,
		acctThrottle: pw.Account,
		rebootHost: func(ctx context.Context, node *store.Node) (int, error) {
			return agent.New(node.Scheme, node.FQDN, node.Port, node.Token).RebootHost(ctx)
		},
	}
}

func (h *NodeHandlers) List(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.store.ListNodes(r.Context())
	if err != nil {
		writeServerError(w, r, "list nodes", err)
		return
	}

	type nodeWithStatus struct {
		*store.Node
		Online bool `json:"online"`
	}

	result := make([]nodeWithStatus, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, nodeWithStatus{Node: n, Online: nodeOnline(n.LastSeen)})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *NodeHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string  `json:"name"`
		FQDN     string  `json:"fqdn"`
		Port     int     `json:"port"`
		Scheme   string  `json:"scheme"`
		Token    string  `json:"token"`
		Location *string `json:"location"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" || body.FQDN == "" || body.Token == "" {
		writeError(w, http.StatusBadRequest, "name, fqdn, and token are required")
		return
	}
	if body.Port == 0 {
		body.Port = 8090
	}
	if body.Scheme == "" {
		body.Scheme = "http"
	}

	n := &store.Node{
		Name:     body.Name,
		FQDN:     body.FQDN,
		Port:     body.Port,
		Scheme:   body.Scheme,
		Location: body.Location,
	}
	created, err := h.store.CreateNode(r.Context(), n, body.Token)
	if err != nil {
		writeServerError(w, r, "create node", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *NodeHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (h *NodeHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	var body struct {
		Name     string  `json:"name"`
		FQDN     string  `json:"fqdn"`
		Port     int     `json:"port"`
		Scheme   string  `json:"scheme"`
		Location *string `json:"location"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name != "" {
		existing.Name = body.Name
	}
	if body.FQDN != "" {
		existing.FQDN = body.FQDN
	}
	if body.Port != 0 {
		existing.Port = body.Port
	}
	if body.Scheme != "" {
		existing.Scheme = body.Scheme
	}
	if body.Location != nil {
		existing.Location = body.Location
	}

	if err := h.store.UpdateNode(r.Context(), id, existing); err != nil {
		writeServerError(w, r, "update node", err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *NodeHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteNode(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNodeHasServers) {
			writeError(w, http.StatusConflict, "node still has servers; delete or move those servers before removing the node")
			return
		}
		writeServerError(w, r, "delete node", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type rebootAccepted struct {
	Status      string    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

type rebootRequest struct {
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// Reboot restarts the machine a node's agent runs on: the agent stops every
// Minecraft server gracefully, then reboots the host.
//
// It is the most disruptive thing the panel can do, so it is gated harder than
// anything else on a node. The router already limits it to global admins and
// refuses every machine principal (access keys and MCP grants); this handler
// adds a fresh password and, when enrolled, TOTP step-up, and records the
// attempt in the audit log whatever the outcome. The agent is the last gate: it
// refuses unless its operator enabled reboots and the OS grants it, and it
// checks both before stopping anything.
func (h *NodeHandlers) Reboot(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body rebootRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	node, err := h.store.GetNode(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if !verifyStepUp(w, r, h.store, h.ipThrottle, h.acctThrottle, uid, body.Password, body.TOTPCode, "node reboot") {
		audit(h.store, r, "", "node.reboot.denied", map[string]any{
			"node_id": node.ID, "node_name": node.Name, "reason": "step-up failed",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, err := h.rebootHost(ctx, node)
	if err != nil {
		slog.Warn("node reboot refused", "node_id", node.ID, "agent_status", status, "error", err)
		audit(h.store, r, "", "node.reboot.failed", map[string]any{
			"node_id": node.ID, "node_name": node.Name, "agent_status": status, "error": err.Error(),
		})
		switch status {
		case http.StatusForbidden, http.StatusPreconditionFailed, http.StatusConflict, http.StatusNotImplemented:
			// The agent's own message says what to change (enable it, grant it,
			// wait for the reboot in progress); pass it through.
			writeError(w, http.StatusConflict, err.Error())
		case http.StatusNotFound:
			writeError(w, http.StatusConflict, "this node's agent is too old to reboot its host; update the agent first")
		default:
			writeError(w, http.StatusBadGateway, "the node's agent could not be reached")
		}
		return
	}

	audit(h.store, r, "", "node.reboot", map[string]any{"node_id": node.ID, "node_name": node.Name})
	// requested_at is the server's clock, the same clock that stamps a node's
	// last_seen. The dashboard compares the host's boot time (last_seen minus
	// uptime) against it to know the machine really restarted, so it must not
	// come from the browser, whose clock may be off.
	writeJSON(w, http.StatusAccepted, rebootAccepted{Status: "rebooting", RequestedAt: time.Now().UTC()})
}

func nodeOnline(lastSeen *time.Time) bool {
	return lastSeen != nil && time.Since(*lastSeen) <= 45*time.Second
}
