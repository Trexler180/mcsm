package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

type NodeHandlers struct {
	store *store.Store
}

func NewNodeHandlers(s *store.Store) *NodeHandlers {
	return &NodeHandlers{store: s}
}

func (h *NodeHandlers) List(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nodeOnline(lastSeen *time.Time) bool {
	return lastSeen != nil && time.Since(*lastSeen) <= 45*time.Second
}
