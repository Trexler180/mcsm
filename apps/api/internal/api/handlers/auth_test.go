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
	if err := s.CreateRefreshToken(ctx, user.ID, oldHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	h := NewAuthHandlers(s, "secret")
	body, _ := json.Marshal(map[string]string{"refresh_token": oldToken})
	rr := httptest.NewRecorder()
	h.Refresh(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" || out.RefreshToken == oldToken {
		t.Fatalf("unexpected token response: %+v", out)
	}
	if _, err := s.GetRefreshToken(ctx, oldHash); err == nil {
		t.Fatal("old refresh token should be invalidated")
	}
	if _, err := s.GetRefreshToken(ctx, hashToken(out.RefreshToken)); err != nil {
		t.Fatalf("new refresh token was not stored: %v", err)
	}
}
