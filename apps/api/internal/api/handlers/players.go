package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// playerActionPermission maps each agent player action to the fine-grained
// permission it requires. It mirrors the agent's closed action set; an action
// outside this map is rejected rather than proxied.
var playerActionPermission = map[string]store.ServerPermission{
	"whitelist_add":    store.ServerPermissionPlayersWhitelist,
	"whitelist_remove": store.ServerPermissionPlayersWhitelist,
	"kick":             store.ServerPermissionPlayersKick,
	"ban":              store.ServerPermissionPlayersBan,
	"pardon":           store.ServerPermissionPlayersBan,
	"ban_ip":           store.ServerPermissionPlayersBan,
	"pardon_ip":        store.ServerPermissionPlayersBan,
	"op":               store.ServerPermissionPlayersOp,
	"deop":             store.ServerPermissionPlayersOp,
}

type PlayersHandlers struct {
	store *store.Store
}

func NewPlayersHandlers(s *store.Store) *PlayersHandlers {
	return &PlayersHandlers{store: s}
}

// proxy registers the server directory with its agent (so playerdata files can
// be read even while the server is offline) and forwards the request to the
// given agent path.
func (h *PlayersHandlers) proxy(w http.ResponseWriter, r *http.Request, agentSuffix string) {
	id := chi.URLParam(r, "id")
	srv, c, ok := serverAgent(w, r, h.store, id)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		writeError(w, http.StatusBadGateway, "failed to register server directory")
		return
	}
	c.ProxyHTTP(ctx, w, r, "/agent/v1/servers/"+srv.ID+agentSuffix)
}

// List proxies the merged player roster (online + offline-from-world-files).
func (h *PlayersHandlers) List(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players")
}

// Detail proxies a single player's parsed .dat data (inventory, stats, etc.).
func (h *PlayersHandlers) Detail(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/"+chi.URLParam(r, "uuid"))
}

// Delete proxies removal of a player's saved data files (gated on players.delete
// by the router). The agent rejects it while the server is running.
func (h *PlayersHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/"+chi.URLParam(r, "uuid"))
}

// Meta proxies the server's Bedrock-bridge info (Geyser/Floodgate install +
// username prefix).
func (h *PlayersHandlers) Meta(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/meta")
}

// Bans proxies the server's consolidated ban state (player + IP bans). Read
// access only — applying a ban goes through Action, which enforces players.ban.
func (h *PlayersHandlers) Bans(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/players/bans")
}

// Sessions returns the panel-tracked play sessions for a server: recent visits
// across everyone, or one player's history plus their summed in-window playtime
// when ?name= is given. ?hours= selects the window (default 7 days).
func (h *PlayersHandlers) Sessions(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	name := r.URL.Query().Get("name")

	hours := 24 * 7
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 24*90 {
			writeError(w, http.StatusBadRequest, "hours must be between 1 and 2160")
			return
		}
		hours = n
	}

	now := time.Now()
	since := now.Add(-time.Duration(hours) * time.Hour)
	sessions, err := h.store.ListPlayerSessions(r.Context(), serverID, name, since, 200)
	if err != nil {
		writeServerError(w, r, "player sessions", err)
		return
	}

	out := map[string]any{
		"hours":    hours,
		"sessions": sessions,
	}
	if name != "" {
		out["playtime_seconds"] = store.PlaytimeFromSessions(sessions, since, now)
	}
	writeJSON(w, http.StatusOK, out)
}

// Action proxies a player administration action (op/ban/whitelist/etc.). Since
// every action shares one route, the specific permission (players.whitelist /
// kick / ban / op) is enforced here from the request body before proxying. The
// agent applies it live via console when the server is up, or by editing the
// config files directly when it is down.
func (h *PlayersHandlers) Action(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var parsed struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	needed, ok := playerActionPermission[parsed.Action]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown player action")
		return
	}
	allowed, err := h.canPerform(r, chi.URLParam(r, "id"), needed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authorization check failed")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Restore the consumed body so proxy() can stream it to the agent.
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	h.proxy(w, r, "/players/action")
}

// canPerform reports whether the caller may perform a player action: global
// admins always may; otherwise the owner/collaborator permission check applies.
func (h *PlayersHandlers) canPerform(r *http.Request, serverID string, needed store.ServerPermission) (bool, error) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		return false, nil
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		return false, err
	}
	if user.Role == "admin" {
		return true, nil
	}
	return h.store.UserHasServerPermission(r.Context(), claims.UserID, serverID, needed)
}
