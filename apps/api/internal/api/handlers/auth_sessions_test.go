package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// login signs u in through the real handler and returns the access token and
// refresh cookie.
func login(t *testing.T, h *AuthHandlers, email string) (string, *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery"})
	rr := httptest.NewRecorder()
	h.Login(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return out.AccessToken, refreshCookieFromResponse(t, rr.Result())
}

func refresh(h *AuthHandlers, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	return rr
}

func sessionOf(t *testing.T, token string) string {
	t.Helper()
	claims, err := auth.ParseAccessToken("secret", token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionID == "" {
		t.Fatal("access token carries no session")
	}
	return claims.SessionID
}

// Two tabs share one cookie jar. When both refresh at once, the one that loses
// the race presents the token the other just rotated away; it must get an
// access token, not log the user out.
func TestRefreshGraceForConcurrentTabs(t *testing.T) {
	f := newFixture(t)
	u := f.user("tabs@example.com", "user")
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	_, cookie := login(t, h, u.Email)

	first := refresh(h, cookie)
	if first.Code != http.StatusOK {
		t.Fatalf("first refresh %d", first.Code)
	}
	second := refresh(h, cookie) // the stale cookie the second tab sent
	if second.Code != http.StatusOK {
		t.Fatalf("a refresh inside the grace window failed with %d: %s", second.Code, second.Body.String())
	}
	if len(second.Result().Cookies()) != 0 {
		t.Fatal("a grace refresh must not touch the cookie the first tab just set")
	}
}

// Outside the grace window, presenting a rotated-away token is reuse of a copy.
// The session ends for everyone holding it.
func TestRefreshReuseEndsSession(t *testing.T) {
	f := newFixture(t)
	u := f.user("reuse@example.com", "user")
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	access, cookie := login(t, h, u.Email)
	sid := sessionOf(t, access)

	if rr := refresh(h, cookie); rr.Code != http.StatusOK {
		t.Fatalf("refresh %d", rr.Code)
	}
	if _, err := f.db.Exec(`UPDATE refresh_tokens SET rotated_at_unix = ? WHERE id = ?`,
		time.Now().Add(-store.RefreshReuseGrace-time.Minute).Unix(), sid); err != nil {
		t.Fatal(err)
	}
	rr := refresh(h, cookie)
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "session_revoked") {
		t.Fatalf("reuse status %d body %s", rr.Code, rr.Body.String())
	}
	if ok, _ := f.store.SessionActive(context.Background(), sid, u.ID); ok {
		t.Fatal("reuse of a rotated refresh token must end the session")
	}
}

// Logout works on the cookie alone, so an expired access token can't stop
// someone signing out, and the session is gone afterwards.
func TestLogoutNeedsOnlyTheCookie(t *testing.T) {
	f := newFixture(t)
	u := f.user("bye@example.com", "user")
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	access, cookie := login(t, h, u.Email)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.Logout(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout %d", rr.Code)
	}
	if ok, _ := f.store.SessionActive(context.Background(), sessionOf(t, access), u.ID); ok {
		t.Fatal("logout left the session alive")
	}
}

// Changing your password ends every other session but keeps the one you used.
func TestChangePasswordEndsOtherSessions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	u := f.user("pw@example.com", "user")
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	mine, _ := login(t, h, u.Email)
	other, _ := login(t, h, u.Email)

	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery", "new_password": "an entirely new password"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewReader(body))
	claims, _ := auth.ParseAccessToken("secret", mine)
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()
	h.ChangePassword(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("change password %d: %s", rr.Code, rr.Body.String())
	}
	if ok, _ := f.store.SessionActive(ctx, sessionOf(t, mine), u.ID); !ok {
		t.Fatal("the session that changed the password should survive")
	}
	if ok, _ := f.store.SessionActive(ctx, sessionOf(t, other), u.ID); ok {
		t.Fatal("other sessions must end when the password changes")
	}

	// A wrong current password changes nothing.
	body, _ = json.Marshal(map[string]string{"current_password": "nope", "new_password": "yet another password"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr = httptest.NewRecorder()
	h.ChangePassword(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password status %d", rr.Code)
	}
}

// An administrator resetting a compromised account's password ends that
// account's sessions, so the attacker's open session goes with the password.
func TestAdminPasswordResetEndsSessions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	victim := f.user("victim@example.com", "user")
	ah := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	stolen, _ := login(t, ah, victim.Email)

	uh := NewUserHandlers(f.store, "secret")
	req := as(admin, "admin", http.MethodPut, "/api/v1/users/"+victim.ID, strings.NewReader(`{"password":"a brand new password"}`))
	rr := httptest.NewRecorder()
	uh.Update(rr, withURLParams(req, "id", victim.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("reset %d: %s", rr.Code, rr.Body.String())
	}
	if ok, _ := f.store.SessionActive(ctx, sessionOf(t, stolen), victim.ID); ok {
		t.Fatal("a password reset must end the account's sessions")
	}
}

// A rejected edit applies nothing: an invalid role must not leave MFA reset
// behind it.
func TestAdminUserUpdateIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	u := f.user("mfa@example.com", "user")
	if err := f.store.SetUserTOTPSecret(ctx, u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnableUserTOTP(ctx, u.ID, []string{"x"}); err != nil {
		t.Fatal(err)
	}

	uh := NewUserHandlers(f.store, "secret")
	req := as(admin, "admin", http.MethodPut, "/", strings.NewReader(`{"disable_mfa":true,"role":"superuser"}`))
	rr := httptest.NewRecorder()
	uh.Update(rr, withURLParams(req, "id", u.ID))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	cfg, _ := f.store.GetUserTOTP(ctx, u.ID)
	if !cfg.Enabled {
		t.Fatal("a rejected update still reset MFA")
	}
}

func TestUserCreateNormalizesEmailAndValidatesRole(t *testing.T) {
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	uh := NewUserHandlers(f.store, "secret")
	create := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		uh.Create(rr, as(admin, "admin", http.MethodPost, "/api/v1/users", strings.NewReader(body)))
		return rr
	}
	if rr := create(`{"email":"  New@Example.com ","password":"long enough pw"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rr.Code, rr.Body.String())
	}
	if rr := create(`{"email":"new@example.com","password":"long enough pw"}`); rr.Code != http.StatusConflict {
		t.Fatalf("a case-only duplicate must conflict, got %d", rr.Code)
	}
	if rr := create(`{"email":"x@example.com","password":"long enough pw","role":"operator"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("an unknown role must be refused, got %d", rr.Code)
	}
	if rr := create(`{"email":"not an email","password":"long enough pw"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("an invalid address must be refused, got %d", rr.Code)
	}
}

func TestUserDeleteGuards(t *testing.T) {
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	uh := NewUserHandlers(f.store, "secret")
	rr := httptest.NewRecorder()
	uh.Delete(rr, withURLParams(as(admin, "admin", http.MethodDelete, "/", nil), "id", admin.ID))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("self-delete status %d, want 400", rr.Code)
	}
}

// Turning MFA off needs the password as well as a code, and a wrong password
// spends from the shared guess budget.
func TestMFADisableRequiresPassword(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	u := f.user("mfa@example.com", "user")
	secret := "JBSWY3DPEHPK3PXP"
	_ = f.store.SetUserTOTPSecret(ctx, u.ID, secret)
	_ = f.store.EnableUserTOTP(ctx, u.ID, []string{"x"})
	code := totpCodeFor(t, secret, time.Now())

	h := NewMFAHandlers(f.store, nil)
	rr := httptest.NewRecorder()
	h.Disable(rr, as(u, "user", http.MethodPost, "/", strings.NewReader(`{"code":"`+code+`"}`)))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disable without a password status %d, want 401", rr.Code)
	}
	if cfg, _ := f.store.GetUserTOTP(ctx, u.ID); !cfg.Enabled {
		t.Fatal("MFA was disabled without the password")
	}

	rr = httptest.NewRecorder()
	h.Disable(rr, as(u, "user", http.MethodPost, "/", strings.NewReader(`{"password":"correct horse battery","code":"`+code+`"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("disable with password and code status %d: %s", rr.Code, rr.Body.String())
	}
}

// Enrolling an authenticator needs the password, so a stolen session cannot
// lock the owner out by enrolling its own.
func TestMFASetupRequiresPassword(t *testing.T) {
	f := newFixture(t)
	u := f.user("setup@example.com", "user")
	h := NewMFAHandlers(f.store, nil)
	rr := httptest.NewRecorder()
	h.Setup(rr, as(u, "user", http.MethodPost, "/", strings.NewReader(`{}`)))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("setup without a password status %d, want 401", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.Setup(rr, as(u, "user", http.MethodPost, "/", strings.NewReader(`{"password":"correct horse battery"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("setup with password status %d", rr.Code)
	}
}
