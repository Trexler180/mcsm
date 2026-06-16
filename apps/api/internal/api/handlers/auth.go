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
	store     *store.Store
	jwtSecret string
	tickets   *auth.TicketStore
}

const refreshCookieName = "mcsm_refresh_token"

var refreshTokenTTL = 7 * 24 * time.Hour

// downloadTicketTTL bounds how long a download/WebSocket ticket is valid. It
// only needs to survive the round trip from issuing the ticket to starting the
// request, so a few seconds is plenty.
const downloadTicketTTL = 30 * time.Second

func NewAuthHandlers(s *store.Store, jwtSecret string, tickets *auth.TicketStore) *AuthHandlers {
	return &AuthHandlers{store: s, jwtSecret: jwtSecret, tickets: tickets}
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil || body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	user, hash, err := h.store.GetUserByEmail(r.Context(), body.Email)
	if err != nil || !auth.CheckPassword(hash, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token error")
		return
	}

	refreshToken, tokenHash, err := generateRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token error")
		return
	}

	expiresAt := time.Now().Add(refreshTokenTTL)
	if err := h.store.CreateRefreshToken(r.Context(), user.ID, tokenHash, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "token storage error")
		return
	}
	setRefreshCookie(w, r, refreshToken, expiresAt)

	_ = h.store.UpdateUserLastLogin(r.Context(), user.ID)
	// Login is a public route (no JWT claims yet), so attribute directly.
	h.store.LogAction(r.Context(), user.ID, "", "auth.login", clientIP(r), nil)

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"user":         user,
	})
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

	if err := h.store.DeleteRefreshTokenByID(r.Context(), rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "token storage error")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token error")
		return
	}

	refreshToken, tokenHash, err := generateRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token error")
		return
	}
	expiresAt := time.Now().Add(refreshTokenTTL)
	if err := h.store.CreateRefreshToken(r.Context(), user.ID, tokenHash, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "token storage error")
		return
	}
	setRefreshCookie(w, r, refreshToken, expiresAt)

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
