package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// APIKeyHandlers owns the agent-access-key lifecycle: list, create, rotate,
// revoke. Every route here is human-only (the router refuses machine
// principals twice over), and the two routes that expose a secret additionally
// require a fresh password and, when the account has it, a current TOTP code.
type APIKeyHandlers struct {
	store *store.Store
	// Step-up reauthentication is a password-guessing surface on an already
	// authenticated session, so it spends from the same budget as login: an
	// aggressive per-IP lockout and a lenient per-account one.
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
}

// NewAPIKeyHandlers builds the access-key handlers. pw is the password-guess
// budget shared with login and the other step-ups; nil gives a private one.
func NewAPIKeyHandlers(s *store.Store, pw *PasswordThrottles) *APIKeyHandlers {
	pw = pw.orNew()
	return &APIKeyHandlers{
		store:        s,
		ipThrottle:   pw.IP,
		acctThrottle: pw.Account,
	}
}

// humanCaller resolves the interactive user behind a lifecycle request, and
// refuses machine principals outright. The router already confines access keys
// to the server routes; repeating the rule at the handler means a future
// re-mount of these routes cannot quietly let a key inspect, mint, or rotate a
// credential. Returns "" and writes a response when the caller is not eligible.
//
// "Machine" here is both families. An MCP grant reaches application code
// through the delegated-actor context rather than as an access key, and
// currentUserID resolves it to the human who owns it — so a check that only
// looked for access keys would read an agent's request as its owner and let a
// delegation approve its own successor. That is the one thing the consent model
// must never allow, so it is refused here as well as at the router.
func humanCaller(w http.ResponseWriter, r *http.Request) string {
	if auth.IsMachine(r.Context()) {
		writeError(w, http.StatusForbidden, "this route requires an interactive sign-in")
		return ""
	}
	uid := currentUserID(r)
	if uid == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return ""
	}
	return uid
}

// keyRequest is the shared body of create and rotate. Recovery codes are
// deliberately absent: they exist to recover a lost second factor at sign-in,
// not to routinely issue long-lived machine credentials.
type keyRequest struct {
	Name      string   `json:"name"`
	ServerIDs []string `json:"server_ids"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"`
	Password  string   `json:"password"`
	TOTPCode  string   `json:"totp_code"`
}

// keySecretResponse is the one and only shape that carries a raw token.
type keySecretResponse struct {
	Key   *store.APIKey `json:"key"`
	Token string        `json:"token"`
}

// List returns the caller's key metadata. Revoked keys are included so the
// owner keeps a record; no response here can contain a hash or a secret,
// because store.APIKey has no field for either.
func (h *APIKeyHandlers) List(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	keys, err := h.store.ListAccessKeys(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "list access keys", err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// Create issues a key and returns its raw token exactly once.
func (h *APIKeyHandlers) Create(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body keyRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.reauthenticate(w, r, uid, body) {
		return
	}

	expiresAt, err := parseKeyExpiry(body.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopes, err := store.NormalizeAccessKeyScopes(body.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, keyValidationMessage(err))
		return
	}
	// The caller must currently hold every requested scope on every selected
	// server. A key can only ever narrow its owner's authority, never widen it.
	if !h.authorizeKeyBounds(w, r, uid, body.ServerIDs, scopes) {
		return
	}

	key, token, err := h.store.CreateAccessKey(r.Context(), uid, body.Name, scopes, body.ServerIDs, expiresAt)
	if err != nil {
		if msg := keyValidationMessage(err); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		writeServerError(w, r, "create access key", err)
		return
	}

	// Audit records what authority was granted, never the secret or its prefix.
	audit(h.store, r, "", "auth.api_key.create", map[string]any{
		"key_id":     key.ID,
		"name":       key.Name,
		"scopes":     key.Scopes,
		"server_ids": key.ServerIDs,
		"expires_at": key.ExpiresAt,
	})
	writeJSON(w, http.StatusCreated, keySecretResponse{Key: key, Token: token})
}

// Rotate replaces a key's secret in place. The previous token stops working the
// moment the update commits.
func (h *APIKeyHandlers) Rotate(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body keyRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.reauthenticate(w, r, uid, body) {
		return
	}

	id := chi.URLParam(r, "id")
	existing, err := h.store.GetAccessKey(r.Context(), id, uid)
	if err != nil {
		// A key id belonging to someone else is reported exactly like one that
		// does not exist.
		writeError(w, http.StatusNotFound, "access key not found")
		return
	}
	// Rotation swaps the secret and keeps every other field, expiry included, so
	// rotating an expired key would mint a token that is dead on arrival. Refuse
	// it here and say what to do instead: an expiry is a deliberate lifetime
	// decision, and silently extending one would be the escalation this whole
	// feature exists to avoid.
	if existing.Expired(time.Now()) {
		writeError(w, http.StatusConflict, "this key has expired; create a new one")
		return
	}
	// Rotation extends a credential's useful life, so re-check that the owner
	// still holds what it grants. An owner who lost a permission must revoke and
	// re-issue rather than quietly refresh a key they can no longer back.
	if !h.authorizeKeyBounds(w, r, uid, existing.ServerIDs, existing.Scopes) {
		return
	}

	key, token, err := h.store.RotateAccessKey(r.Context(), id, uid)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAccessKeyNotFound):
			writeError(w, http.StatusNotFound, "access key not found")
		case errors.Is(err, store.ErrAccessKeyRevoked):
			writeError(w, http.StatusConflict, "this key is revoked; create a new one")
		default:
			writeServerError(w, r, "rotate access key", err)
		}
		return
	}

	audit(h.store, r, "", "auth.api_key.rotate", map[string]any{
		"key_id": key.ID,
		"name":   key.Name,
	})
	writeJSON(w, http.StatusOK, keySecretResponse{Key: key, Token: token})
}

// Revoke soft-revokes the caller's key. Idempotent: revoking twice is a 204
// both times, so a retried request never surprises the caller.
func (h *APIKeyHandlers) Revoke(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.store.RevokeAccessKey(r.Context(), id, uid); err != nil {
		if errors.Is(err, store.ErrAccessKeyNotFound) {
			writeError(w, http.StatusNotFound, "access key not found")
			return
		}
		writeServerError(w, r, "revoke access key", err)
		return
	}
	audit(h.store, r, "", "auth.api_key.revoke", map[string]any{"key_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// reauthenticate enforces the step-up: a current password and, when the account
// has MFA enabled, a current TOTP code. Every failure returns the same generic
// 401 so the response can't be used to tell a wrong password from a wrong code.
// Returns false when it has already written a response.
func (h *APIKeyHandlers) reauthenticate(w http.ResponseWriter, r *http.Request, userID string, body keyRequest) bool {
	ipKey := passwordIPKey(r)
	acctKey := stepUpAccountKey(userID)
	if ok, retry := h.ipThrottle.Allowed(ipKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	if ok, retry := h.acctThrottle.Allowed(acctKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	fail := func() {
		h.ipThrottle.Fail(ipKey)
		h.acctThrottle.Fail(acctKey)
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
	}

	if body.Password == "" {
		// Still a failed attempt: an empty password must not be a free probe.
		fail()
		return false
	}
	hash, err := h.store.GetUserPasswordHash(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
		return false
	}
	if !auth.CheckPassword(hash, body.Password) {
		fail()
		return false
	}

	cfg, err := h.store.GetUserTOTP(r.Context(), userID)
	if err != nil {
		writeServerError(w, r, "access key: totp lookup", err)
		return false
	}
	if cfg.Enabled && !auth.ValidateTOTP(cfg.Secret, body.TOTPCode, time.Now()) {
		fail()
		return false
	}

	h.ipThrottle.Reset(ipKey)
	h.acctThrottle.Reset(acctKey)
	return true
}

// authorizeKeyBounds verifies the caller currently holds every scope on every
// listed server. A global admin satisfies the RBAC term (as they do everywhere
// else), but that does not widen the key — the key stays bounded by exactly the
// servers and scopes recorded on it.
//
// Returns false when it has already written a response.
func (h *APIKeyHandlers) authorizeKeyBounds(w http.ResponseWriter, r *http.Request, userID string, serverIDs, scopes []string) bool {
	if len(serverIDs) == 0 {
		writeError(w, http.StatusBadRequest, "select at least one server")
		return false
	}
	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	isAdmin := user.Role == "admin"

	// One status and one message for "no such server" and "you don't hold that
	// there", so the form can't be used to enumerate the rest of the fleet.
	reject := func() {
		writeError(w, http.StatusBadRequest, "unknown server, or you do not have that access on it")
	}

	for _, serverID := range serverIDs {
		if _, err := h.store.GetServer(r.Context(), serverID); err != nil {
			reject()
			return false
		}
		if isAdmin {
			continue
		}
		for _, scope := range scopes {
			ok, err := h.store.UserHasServerPermission(r.Context(), userID, serverID, store.ServerPermission(scope))
			if err != nil {
				writeServerError(w, r, "access key: permission check", err)
				return false
			}
			if !ok {
				reject()
				return false
			}
		}
	}
	return true
}

// parseKeyExpiry accepts an RFC 3339 timestamp and enforces the mandatory
// bounded lifetime.
func parseKeyExpiry(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, errors.New("an expiry date is required")
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("expiry must be an RFC 3339 timestamp")
	}
	if err := store.ValidateAccessKeyExpiry(expiresAt, time.Now()); err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

// keyValidationMessage maps the store's typed validation errors onto safe
// client-facing text, and returns "" for anything that is not a validation
// failure (which the caller then treats as a 500).
func keyValidationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrAccessKeyAdminScope),
		errors.Is(err, store.ErrAccessKeyNoScopes),
		errors.Is(err, store.ErrAccessKeyNoServers),
		errors.Is(err, store.ErrAccessKeyNameRequired),
		errors.Is(err, store.ErrAccessKeyExpiryMissing),
		errors.Is(err, store.ErrAccessKeyExpiryPast),
		errors.Is(err, store.ErrAccessKeyExpiryTooFar),
		errors.Is(err, store.ErrInvalidServerPermission):
		return err.Error()
	}
	return ""
}
