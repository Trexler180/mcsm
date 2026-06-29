package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type AuthHandlers struct {
	store        *store.Store
	jwtSecret    string
	tickets      *auth.TicketStore
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
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

func NewAuthHandlers(s *store.Store, jwtSecret string, tickets *auth.TicketStore) *AuthHandlers {
	return &AuthHandlers{
		store:        s,
		jwtSecret:    jwtSecret,
		tickets:      tickets,
		ipThrottle:   auth.NewLoginThrottle(),
		acctThrottle: auth.NewAccountThrottle(),
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
	ipKey := "ip:" + clientIP(r)
	acctKey := "acct:" + strings.ToLower(strings.TrimSpace(body.Email))
	if ok, retry := h.ipThrottle.Allowed(ipKey); !ok {
		tooManyRequests(w, retry)
		return
	}
	if ok, retry := h.acctThrottle.Allowed(acctKey); !ok {
		tooManyRequests(w, retry)
		return
	}
	failAuth := func() {
		h.ipThrottle.Fail(ipKey)
		h.acctThrottle.Fail(acctKey)
	}

	user, hash, err := h.store.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		// Spend equivalent CPU on a missing account so response time can't be used
		// to tell "no such user" from "wrong password".
		auth.DummyCheck(body.Password)
		failAuth()
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(hash, body.Password) {
		failAuth()
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Second factor, when the account has TOTP enabled.
	cfg, err := h.store.GetUserTOTP(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, r, "login: totp lookup", err)
		return
	}
	if cfg.Enabled {
		switch {
		case body.TOTPCode != "":
			if !auth.ValidateTOTP(cfg.Secret, body.TOTPCode, time.Now()) {
				failAuth()
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
				failAuth()
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
	// Login is a public route (no JWT claims yet), so attribute directly.
	h.store.LogAction(r.Context(), user.ID, "", "auth.login", clientIP(r), nil)

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"user":         user,
	})
}

// startSession mints an access token, creates a new refresh-token session row
// recording the caller's device, and sets the refresh cookie.
func (h *AuthHandlers) startSession(w http.ResponseWriter, r *http.Request, user *store.User) (string, error) {
	accessToken, err := auth.IssueAccessToken(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		return "", err
	}
	refreshToken, tokenHash, err := generateRefreshToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(refreshTokenTTL)
	if _, err := h.store.CreateRefreshToken(r.Context(), user.ID, tokenHash, userAgent(r), clientIP(r), expiresAt); err != nil {
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

	rt, err := h.store.GetRefreshToken(r.Context(), hashToken(refreshToken))
	if err != nil {
		clearRefreshCookie(w, r)
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	user, err := h.store.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		writeServerError(w, r, "refresh: token", err)
		return
	}

	// Rotate the token in place: the session keeps its identity (and original
	// created_at) across refreshes, so the sessions list shows one row per login
	// rather than churning a new one every 15 minutes. A stolen-and-rotated token
	// invalidates the victim's copy, which surfaces as a forced re-login.
	newToken, newHash, err := generateRefreshToken()
	if err != nil {
		writeServerError(w, r, "refresh: rotate", err)
		return
	}
	expiresAt := time.Now().Add(refreshTokenTTL)
	if err := h.store.RotateRefreshToken(r.Context(), rt.ID, newHash, clientIP(r), userAgent(r), expiresAt); err != nil {
		writeServerError(w, r, "refresh: store", err)
		return
	}
	setRefreshCookie(w, r, newToken, expiresAt)

	writeJSON(w, http.StatusOK, map[string]string{
		"access_token": accessToken,
	})
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken := refreshTokenFromRequest(r)
	if refreshToken != "" {
		_ = h.store.DeleteRefreshToken(r.Context(), hashToken(refreshToken))
	}
	clearRefreshCookie(w, r)
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
		writeError(w, http.StatusNotFound, "user not found")
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
	ticket, err := h.tickets.Issue(claims, downloadTicketTTL)
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
