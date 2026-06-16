package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTicketOnlyAllowedForStreamingEndpoints(t *testing.T) {
	secret := "secret"
	tickets := NewTicketStore()
	claims := &Claims{UserID: "user-1", Email: "u@example.com", Role: "user"}

	var called bool
	handler := Middleware(secret, tickets)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	// A valid ticket authenticates a streaming endpoint.
	ticket, err := tickets.Issue(claims, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRequest(http.MethodGet, "/api/v1/servers/srv/console?ticket="+ticket, nil)
	handler.ServeHTTP(httptest.NewRecorder(), allowed)
	if !called {
		t.Fatal("expected console ticket to authenticate")
	}

	// Tickets are single-use: replaying the same one fails.
	called = false
	replay := httptest.NewRequest(http.MethodGet, "/api/v1/servers/srv/console?ticket="+ticket, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, replay)
	if called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("replayed ticket status=%d called=%v, want unauthorized and not called", rr.Code, called)
	}

	// A raw JWT in the query string is never accepted.
	jwtTok, err := IssueAccessToken(secret, "user-1", "u@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	called = false
	rawJWT := httptest.NewRequest(http.MethodGet, "/api/v1/servers/srv/console?token="+jwtTok, nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, rawJWT)
	if called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("raw JWT query status=%d called=%v, want unauthorized and not called", rr.Code, called)
	}

	// Tickets are not honored on ordinary endpoints.
	ticket2, _ := tickets.Issue(claims, time.Minute)
	called = false
	denied := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me?ticket="+ticket2, nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, denied)
	if called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("ticket on ordinary endpoint status=%d called=%v, want unauthorized and not called", rr.Code, called)
	}
}
