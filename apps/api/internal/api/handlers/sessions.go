package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

type SessionHandlers struct {
	store *store.Store
}

func NewSessionHandlers(s *store.Store) *SessionHandlers {
	return &SessionHandlers{store: s}
}

// currentSessionID resolves the caller's own session row from the refresh cookie
// (sent on /api/v1/auth/* paths), so the list can flag it and "log out others"
// can spare it. Returns "" when no/invalid cookie is present.
func (h *SessionHandlers) currentSessionID(r *http.Request) string {
	tok := refreshTokenFromRequest(r)
	if tok == "" {
		return ""
	}
	rt, err := h.store.GetRefreshToken(r.Context(), hashToken(tok))
	if err != nil {
		return ""
	}
	return rt.ID
}

// List returns the caller's active sessions, flagging the one making the request.
func (h *SessionHandlers) List(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	sessions, err := h.store.ListSessions(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "list sessions", err)
		return
	}
	current := h.currentSessionID(r)
	for _, s := range sessions {
		if s.ID == current {
			s.Current = true
		}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// Revoke deletes one of the caller's sessions by id.
func (h *SessionHandlers) Revoke(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	id := chi.URLParam(r, "id")
	ok, err := h.store.DeleteSessionForUser(r.Context(), id, uid)
	if err != nil {
		writeServerError(w, r, "revoke session", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// If they revoked the session they're using, clear its cookie too.
	if id == h.currentSessionID(r) {
		clearRefreshCookie(w, r)
	}
	audit(h.store, r, "", "auth.session_revoked", map[string]any{"session_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOthers logs out every session except the one making the request — the
// "sign out everywhere else" action.
func (h *SessionHandlers) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	current := h.currentSessionID(r)
	if err := h.store.DeleteSessionsForUserExcept(r.Context(), uid, current); err != nil {
		writeServerError(w, r, "revoke other sessions", err)
		return
	}
	audit(h.store, r, "", "auth.sessions_revoked_others", nil)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
