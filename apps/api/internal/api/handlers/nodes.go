package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
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

// nodeInput is the editable part of a node, shared by create and update.
type nodeInput struct {
	Name     *string `json:"name"`
	FQDN     *string `json:"fqdn"`
	Port     *int    `json:"port"`
	Scheme   *string `json:"scheme"`
	Token    *string `json:"token"`
	Location *string `json:"location"`
}

// validateNode checks a node's address and name, writing a field error and
// returning false when something is wrong.
func validateNode(w http.ResponseWriter, n *store.Node) bool {
	n.Name = strings.TrimSpace(n.Name)
	n.FQDN = strings.TrimSpace(n.FQDN)
	switch {
	case n.Name == "" || len(n.Name) > 60:
		writeFieldError(w, "name", "name is required and must be at most 60 characters")
	case n.FQDN == "" || strings.ContainsAny(n.FQDN, "/:?#@ ") && net.ParseIP(n.FQDN) == nil:
		writeFieldError(w, "fqdn", "fqdn must be a bare host name or IP address, with no scheme, port or path")
	case n.Port < 1 || n.Port > 65535:
		writeFieldError(w, "port", "port must be between 1 and 65535")
	case n.Scheme != "http" && n.Scheme != "https":
		writeFieldError(w, "scheme", "scheme must be http or https")
	default:
		return true
	}
	return false
}

func (h *NodeHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var body nodeInput
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	n := &store.Node{Port: 8090, Scheme: "http", Location: body.Location}
	if body.Name != nil {
		n.Name = *body.Name
	}
	if body.FQDN != nil {
		n.FQDN = *body.FQDN
	}
	if body.Port != nil {
		n.Port = *body.Port
	}
	if body.Scheme != nil {
		n.Scheme = *body.Scheme
	}
	if !validateNode(w, n) {
		return
	}
	if body.Token == nil || strings.TrimSpace(*body.Token) == "" {
		writeFieldError(w, "token", "token is required")
		return
	}
	created, err := h.store.CreateNode(r.Context(), n, strings.TrimSpace(*body.Token))
	if err != nil {
		writeServerError(w, r, "create node", err)
		return
	}
	audit(h.store, r, "", "node.create", map[string]any{
		"node_id": created.ID, "name": created.Name, "address": nodeAddress(created),
	})
	writeJSON(w, http.StatusCreated, created)
}

func (h *NodeHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "node", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// Update edits a node. Changing where the node is — host, port or scheme —
// requires its token in the same request. The API sends that token with every
// call to the node, so without this, editing the address would hand the token
// to whatever host the new address names on the next poll: a hijacked admin
// session could not read the token, but could have it delivered.
func (h *NodeHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "node", err)
		return
	}
	before := *existing

	var body nodeInput
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.FQDN != nil {
		existing.FQDN = *body.FQDN
	}
	if body.Port != nil {
		existing.Port = *body.Port
	}
	if body.Scheme != nil {
		existing.Scheme = *body.Scheme
	}
	if body.Location != nil {
		existing.Location = body.Location
	}
	if !validateNode(w, existing) {
		return
	}
	addressChanged := existing.FQDN != before.FQDN || existing.Port != before.Port || existing.Scheme != before.Scheme
	token := ""
	if body.Token != nil {
		token = strings.TrimSpace(*body.Token)
	}
	if addressChanged && token == "" {
		writeFieldError(w, "token", "enter the node's token to change its address")
		return
	}

	if err := h.store.UpdateNode(r.Context(), id, existing); err != nil {
		writeServerError(w, r, "update node", err)
		return
	}
	if token != "" {
		if err := h.store.SetNodeToken(r.Context(), id, token); err != nil {
			writeServerError(w, r, "update node token", err)
			return
		}
	}
	changes := map[string]any{}
	if existing.Name != before.Name {
		changes["name"] = map[string]string{"from": before.Name, "to": existing.Name}
	}
	if addressChanged {
		changes["address"] = map[string]string{"from": nodeAddress(&before), "to": nodeAddress(existing)}
	}
	if token != "" {
		changes["token"] = "replaced"
	}
	if len(changes) > 0 {
		audit(h.store, r, "", "node.update", map[string]any{"node_id": id, "changes": changes})
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *NodeHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	node, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		writeLookupError(w, r, "node", err)
		return
	}
	if err := h.store.DeleteNode(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNodeHasServers) {
			writeError(w, http.StatusConflict, "node still has servers; delete or move those servers before removing the node")
			return
		}
		writeServerError(w, r, "delete node", err)
		return
	}
	audit(h.store, r, "", "node.delete", map[string]any{"node_id": id, "name": node.Name, "address": nodeAddress(node)})
	w.WriteHeader(http.StatusNoContent)
}

// nodeAddress renders where a node is, for the audit log. Never the token.
func nodeAddress(n *store.Node) string {
	return fmt.Sprintf("%s://%s", n.Scheme, net.JoinHostPort(n.FQDN, fmt.Sprint(n.Port)))
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
		writeLookupError(w, r, "node", err)
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
