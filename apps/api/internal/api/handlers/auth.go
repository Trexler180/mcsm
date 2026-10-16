package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

type AuthHandlers struct {
	store        *store.Store
	jwtSecret    string
	tickets      *auth.TicketStore
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
	notifier     *notify.Engine
}

// WithNotifier enables the new-device sign-in alert.
func (h *AuthHandlers) WithNotifier(n *notify.Engine) *AuthHandlers {
	h.notifier = n
	return h
}

const refreshCookieName = "mcsm_refresh_token"

// recoveryCodeCount is how many one-time recovery codes are issued when a user
// enables MFA.
const recoveryCodeCount = 10

var refreshTokenTTL = 7 * 24 * time.Hour

// downloadTicketTTL bounds how long a download/WebSocket ticket is valid. It
// only needs to survive the round trip from issuing the ticket to starting the
// request, so a few seconds is plenty.
const downloadTicketTTL = 30 * time.Second

// NewAuthHandlers builds the login handlers. pw is the password-guess budget
// shared with every step-up; nil gives this handler a private one.
func NewAuthHandlers(s *store.Store, jwtSecret string, tickets *auth.TicketStore, pw *PasswordThrottles) *AuthHandlers {
	pw = pw.orNew()
	return &AuthHandlers{
		store:        s,
		jwtSecret:    jwtSecret,
		tickets:      tickets,
		ipThrottle:   pw.IP,
		acctThrottle: pw.Account,
	}
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		TOTPCode     string `json:"totp_code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := decode(r, &body); err != nil || body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	// Brute-force defense: an aggressive per-IP lockout stops a single attacker,
	// and a lenient short-window per-account lockout slows a distributed guess
	// without letting anyone deny a real user access to their account for long.
	ipKey := passwordIPKey(r)
	acctKey := "acct:" + strings.ToLower(strings.TrimSpace(body.Email))
	if ok, retry := h.ipThrottle.Allowed(ipKey); !ok {
		tooManyRequests(w, retry)
		return
	}
	if ok, retry := h.acctThrottle.Allowed(acctKey); !ok {
		tooManyRequests(w, retry)
		return
	}
	failAuth := func(reason, userID string) {
		h.ipThrottle.Fail(ipKey)
		h.acctThrottle.Fail(acctKey)
		// The address is recorded as a short hash, never in the clear: the log
		// should show a burst against one account without becoming a list of
		// every address anyone has mistyped.
		h.store.LogAction(r.Context(), userID, "", "auth.login_failed", clientIP(r), map[string]any{
			"reason":        reason,
			"account_match": userID != "",
			"email_hash":    emailFingerprint(body.Email),
		})
	}

	user, hash, err := h.store.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		// Spend equivalent CPU on a missing account so response time can't be used
		// to tell "no such user" from "wrong password".
		auth.DummyCheck(body.Password)
		failAuth("unknown_account", "")
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(hash, body.Password) {
		failAuth("bad_password", user.ID)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Second factor, when the account has TOTP enabled.
	cfg, err := h.store.GetUserTOTP(r.Context(), user.ID)
	if errors.Is(err, store.ErrSecretUnreadable) {
		slog.Error("login: this account's second factor cannot be read; the database was likely restored without its encryption key",
			"user_id", user.ID, "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Sign-in is unavailable for this account: the panel cannot read its two-factor secret. " +
				"An administrator needs to restore the panel's encryption key.",
			"code":      "encryption_key_mismatch",
			"retryable": false,
		})
		return
	}
	if err != nil {
		writeServerError(w, r, "login: totp lookup", err)
		return
	}
	if cfg.Enabled {
		switch {
		case body.TOTPCode != "":
			if !auth.ValidateTOTP(cfg.Secret, body.TOTPCode, time.Now()) {
				failAuth("bad_totp", user.ID)
				writeError(w, http.StatusUnauthorized, "invalid authentication code")
				return
			}
		case body.RecoveryCode != "":
			ok, err := h.store.ConsumeRecoveryCode(r.Context(), user.ID, auth.NormalizeRecoveryCode(body.RecoveryCode))
			if err != nil {
				writeServerError(w, r, "login: recovery code", err)
				return
			}
			if !ok {
				failAuth("bad_recovery_code", user.ID)
				writeError(w, http.StatusUnauthorized, "invalid recovery code")
				return
			}
		default:
			// Password is correct but a second factor is needed. This is the normal
			// two-step handshake, not a failed attempt, so don't burn the throttle.
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":        "mfa_required",
				"mfa_required": true,
			})
			return
		}
	}

	h.ipThrottle.Reset(ipKey)
	h.acctThrottle.Reset(acctKey)

	accessToken, err := h.startSession(w, r, user)
	if err != nil {
		writeServerError(w, r, "login: issue session", err)
		return
	}

	_ = h.store.UpdateUserLastLogin(r.Context(), user.ID)
	device := deviceFingerprint(userAgent(r))
	newDevice := h.isNewDevice(r, user.ID, device)
	// Login is a public route (no JWT claims yet), so attribute directly.
	h.store.LogAction(r.Context(), user.ID, "", "auth.login", clientIP(r), map[string]any{"device": device})
	if newDevice {
		h.notifier.EmitToUser(user.ID, notify.NewDeviceLogin(clientIP(r), deviceLabel(userAgent(r))))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"user":         user,
	})
}

// startSession creates a refresh-token session row recording the caller's
// device, sets the refresh cookie, and mints an access token bound to that
// session.
func (h *AuthHandlers) startSession(w http.ResponseWriter, r *http.Request, user *store.User) (string, error) {
	refreshToken, tokenHash, err := generateRefreshToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(refreshTokenTTL)
	sessionID, err := h.store.CreateRefreshToken(r.Context(), user.ID, tokenHash, userAgent(r), clientIP(r), expiresAt)
	if err != nil {
		return "", err
	}
	accessToken, err := auth.IssueSessionToken(h.jwtSecret, user.ID, user.Email, user.Role, sessionID)
	if err != nil {
		return "", err
	}
	setRefreshCookie(w, r, refreshToken, expiresAt)
	return accessToken, nil
}

func (h *AuthHandlers) Refresh(w http.ResponseWriter, r *http.Request) {
	refreshToken := refreshTokenFromRequest(r)
	if refreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token required")
		return
	}

	rt, grace, err := h.store.LookupRefreshToken(r.Context(), hashToken(refreshToken))
	if errors.Is(err, store.ErrRefreshTokenReused) {
		// A token rotated away long ago is being replayed: only a copy can do
		// that. End the session so neither holder keeps it.
		_ = h.store.DeleteRefreshTokenByID(r.Context(), rt.ID)
		_ = h.store.LogAction(r.Context(), rt.UserID, "", "auth.refresh_reuse_detected", clientIP(r), map[string]any{"session_id": rt.ID})
		clearRefreshCookie(w, r)
		writeCodedError(w, http.StatusUnauthorized, "session_revoked", "this session was ended because its refresh token was reused")
		return
	}
	if err != nil {
		clearRefreshCookie(w, r)
		writeCodedError(w, http.StatusUnauthorized, "session_revoked", "invalid or expired refresh token")
		return
	}

	user, err := h.store.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeCodedError(w, http.StatusUnauthorized, "session_revoked", "user not found")
		return
	}

	accessToken, err := auth.IssueSessionToken(h.jwtSecret, user.ID, user.Email, user.Role, rt.ID)
	if err != nil {
		writeServerError(w, r, "refresh: token", err)
		return
	}

	if grace {
		// Another tab rotated this token a moment ago and the browser already
		// holds the new cookie. Answer with an access token and leave the
		// cookie alone; rotating again would log the other tab out.
		writeJSON(w, http.StatusOK, map[string]string{"access_token": accessToken})
		return
	}

	// Rotate the token in place: the session keeps its identity (and original
	// created_at) across refreshes, so the sessions list shows one row per login
	// rather than churning a new one every 15 minutes.
	newToken, newHash, err := generateRefreshToken()
	if err != nil {
		writeServerError(w, r, "refresh: rotate", err)
		return
	}
	expiresAt := time.Now().Add(refreshTokenTTL)
	if err := h.store.RotateRefreshToken(r.Context(), rt.ID, rt.TokenHash, newHash, clientIP(r), userAgent(r), expiresAt); err != nil {
		if errors.Is(err, store.ErrRefreshTokenAlreadyRotated) {
			// Lost a race with a concurrent refresh of the same token: that
			// request rotated it and set the new cookie, so this one is a
			// retry inside the grace window.
			writeJSON(w, http.StatusOK, map[string]string{"access_token": accessToken})
			return
		}
		writeServerError(w, r, "refresh: store", err)
		return
	}
	setRefreshCookie(w, r, newToken, expiresAt)

	writeJSON(w, http.StatusOK, map[string]string{
		"access_token": accessToken,
	})
}

// Logout ends the session named by the refresh cookie. It is a public route:
// an expired access token must not stop someone signing out, and the cookie is
// what identifies the session anyway.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if refreshToken := refreshTokenFromRequest(r); refreshToken != "" {
		rt, _, err := h.store.LookupRefreshToken(r.Context(), hashToken(refreshToken))
		if rt != nil && (err == nil || errors.Is(err, store.ErrRefreshTokenReused)) {
			_ = h.store.DeleteRefreshTokenByID(r.Context(), rt.ID)
			_ = h.store.LogAction(r.Context(), rt.UserID, "", "auth.logout", clientIP(r), map[string]any{"session_id": rt.ID})
		}
	}
	clearRefreshCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// ChangePassword lets a signed-in user set a new password. It needs the current
// password and, when MFA is on, a current code — the same step-up, from the
// same guess budget, as every other sensitive action — and it ends every other
// session the account has, since a password change is what someone does when
// they think the old one leaked.
func (h *AuthHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		TOTPCode        string `json:"totp_code"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg, ok := validatePassword(body.NewPassword); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !verifyStepUp(w, r, h.store, h.ipThrottle, h.acctThrottle, uid, body.CurrentPassword, body.TOTPCode, "password change") {
		return
	}
	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		writeServerError(w, r, "password change: hash", err)
		return
	}
	if err := h.store.UpdateUserPassword(r.Context(), uid, hash); err != nil {
		writeServerError(w, r, "password change: store", err)
		return
	}
	if err := h.store.DeleteSessionsForUserExcept(r.Context(), uid, currentSessionID(r)); err != nil {
		writeServerError(w, r, "password change: end sessions", err)
		return
	}
	audit(h.store, r, "", "auth.password_changed", nil)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		writeLookupError(w, r, "user", err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// UpdateMe changes what the signed-in person can change about themselves:
// their display name. Email and role stay with an administrator.
func (h *AuthHandlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		DisplayName *string `json:"display_name"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name, err := cleanDisplayName(body.DisplayName)
	if err != nil {
		writeFieldError(w, "display_name", err.Error())
		return
	}
	if name != nil {
		if _, err := h.store.UpdateUserAdmin(r.Context(), claims.UserID, store.UserAdminUpdate{DisplayName: name}); err != nil {
			writeServerError(w, r, "update me", err)
			return
		}
		audit(h.store, r, "", "user.update", map[string]any{"user_id": claims.UserID, "changes": map[string]any{"display_name": *name}})
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		writeLookupError(w, r, "user", err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// Ticket issues a short-lived, single-use ticket for endpoints that can't carry
// an Authorization header (file downloads, console/metrics WebSockets). The
// caller is already authenticated via the Bearer header on this request; the
// ticket simply stands in for that identity on the follow-up navigation.
func (h *AuthHandlers) Ticket(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// A machine access key is never converted into a query-string credential.
	// The route boundary already keeps keys off /auth/*; stating it here as well
	// means a future re-mount of this handler can't silently open that path.
	if auth.MachineFrom(r.Context()) != nil {
		writeError(w, http.StatusForbidden, "access keys cannot mint tickets; send the key in the Authorization header")
		return
	}
	var body struct {
		// Path is the exact request path the ticket will be presented on.
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil || !auth.TicketPathAllowed(body.Path) {
		writeError(w, http.StatusBadRequest, "path must name a console, metrics, notification stream, or download endpoint")
		return
	}
	ticket, err := h.tickets.Issue(claims, body.Path, downloadTicketTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ticket error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"expires_in": int(downloadTicketTTL.Seconds()),
	})
}

func generateRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	token = hex.EncodeToString(b)
	hash = hashToken(token)
	return
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func refreshTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(refreshCookieName); err == nil {
		return c.Value
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = decode(r, &body)
	if body.RefreshToken != "" {
		return body.RefreshToken
	}
	return ""
}

func setRefreshCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     "/api/v1/auth",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearRefreshCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// deviceFingerprint identifies the kind of browser and system a sign-in came
// from, stable across browser updates: the user agent with its version
// numbers removed, hashed so the audit log carries no raw user agent.
func deviceFingerprint(ua string) string {
	stripped := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == '_' {
			return -1
		}
		return r
	}, strings.ToLower(ua))
	sum := sha256.Sum256([]byte(stripped))
	return hex.EncodeToString(sum[:8])
}

// deviceLabel is a short readable description of a user agent for the alert.
func deviceLabel(ua string) string {
	lower := strings.ToLower(ua)
	browser := "a browser"
	for _, b := range []struct{ token, name string }{
		{"edg/", "Edge"}, {"firefox/", "Firefox"}, {"chrome/", "Chrome"}, {"safari/", "Safari"},
	} {
		if strings.Contains(lower, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	for _, o := range []struct{ token, name string }{
		{"windows", "Windows"}, {"android", "Android"}, {"iphone", "iOS"}, {"ipad", "iPadOS"},
		{"mac os", "macOS"}, {"linux", "Linux"},
	} {
		if strings.Contains(lower, o.token) {
			system = o.name
			break
		}
	}
	if system == "" {
		return browser
	}
	return browser + " on " + system
}

// isNewDevice reports whether this account has signed in before, but never
// from this kind of device. A first-ever sign-in is not "new device".
func (h *AuthHandlers) isNewDevice(r *http.Request, userID, device string) bool {
	if h.notifier == nil {
		return false
	}
	before, seen, err := h.store.LoginDeviceHistory(r.Context(), userID, device)
	return err == nil && before && !seen
}
