package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/publicurl"
)

// Every password check spends from one budget. Guesses made through a step-up
// on a stolen session must count against login from the same address — and
// against the other step-ups — rather than each endpoint granting its own set
// of free attempts.
func TestPasswordGuessesShareOneBudgetAcrossEndpoints(t *testing.T) {
	ctx := context.Background()
	s := authTestStore(t)
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "owner@example.com", hash, "user")
	if err != nil {
		t.Fatal(err)
	}

	pw := NewPasswordThrottles()
	keys := NewAPIKeyHandlers(s, pw)
	grants := NewMCPGrantHandlers(s, publicurl.Config{}, pw)
	login := NewAuthHandlers(s, "secret", auth.NewTicketStore(), pw)

	const addr = "198.51.100.9:5000"
	stepUp := func() *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = addr
		return r
	}

	// Spend the free attempts, split across the two step-up endpoints.
	for i := range 6 {
		w := httptest.NewRecorder()
		if i%2 == 0 {
			keys.reauthenticate(w, stepUp(), user.ID, keyRequest{Password: "wrong"})
		} else {
			grants.reauthenticate(w, stepUp(), user.ID, approvalRequest{Password: "wrong"})
		}
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("locked out after only %d failures", i)
		}
	}

	// Login from the same address is now locked, even with the right password.
	r := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"owner@example.com","password":"correct horse battery staple"}`))
	r.RemoteAddr = addr
	w := httptest.NewRecorder()
	login.Login(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("login after step-up failures: status=%d, want 429", w.Code)
	}

	// And so is the other step-up.
	w = httptest.NewRecorder()
	if keys.reauthenticate(w, stepUp(), user.ID, keyRequest{Password: "correct horse battery staple"}) {
		t.Fatal("a step-up succeeded while the shared budget was locked")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("step-up while locked: status=%d, want 429", w.Code)
	}
}
