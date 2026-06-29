package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func authTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	return store.New(db)
}

func TestRefreshRotatesRefreshToken(t *testing.T) {
	ctx := context.Background()
	s := authTestStore(t)
	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	oldToken, oldHash, err := generateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := func() error { _, e := s.CreateRefreshToken(ctx, user.ID, oldHash, "", "", time.Now().Add(time.Hour)); return e }(); err != nil {
		t.Fatal(err)
	}

	h := NewAuthHandlers(s, "secret", auth.NewTicketStore())
	body, _ := json.Marshal(map[string]string{"refresh_token": oldToken})
	rr := httptest.NewRecorder()
	h.Refresh(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.AccessToken == "" {
		t.Fatalf("unexpected token response: %+v", out)
	}
	cookie := refreshCookieFromResponse(t, rr.Result())
	if cookie.Value == "" || cookie.Value == oldToken {
		t.Fatalf("unexpected refresh cookie: %+v", cookie)
	}
	if !cookie.HttpOnly {
		t.Fatal("refresh cookie should be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("refresh cookie SameSite=%v, want Strict", cookie.SameSite)
	}
	if _, err := s.GetRefreshToken(ctx, oldHash); err == nil {
		t.Fatal("old refresh token should be invalidated")
	}
	if _, err := s.GetRefreshToken(ctx, hashToken(cookie.Value)); err != nil {
		t.Fatalf("new refresh token was not stored: %v", err)
	}
}

func TestRefreshAcceptsCookieToken(t *testing.T) {
	ctx := context.Background()
	s := authTestStore(t)
	user, err := s.CreateUser(ctx, "owner@example.com", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	oldToken, oldHash, err := generateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := func() error { _, e := s.CreateRefreshToken(ctx, user.ID, oldHash, "", "", time.Now().Add(time.Hour)); return e }(); err != nil {
		t.Fatal(err)
	}

	h := NewAuthHandlers(s, "secret", auth.NewTicketStore())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: oldToken})
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if cookie := refreshCookieFromResponse(t, rr.Result()); cookie.Value == "" || cookie.Value == oldToken {
		t.Fatalf("unexpected refresh cookie: %+v", cookie)
	}
}

func refreshCookieFromResponse(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == refreshCookieName {
			return c
		}
	}
	t.Fatal("refresh cookie was not set")
	return nil
}
