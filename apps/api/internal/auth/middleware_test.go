package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryTokenOnlyAllowedForStreamingEndpoints(t *testing.T) {
	secret := "secret"
	token, err := IssueAccessToken(secret, "user-1", "u@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	var called bool
	handler := Middleware(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRequest(http.MethodGet, "/api/v1/servers/srv/console?token="+token, nil)
	handler.ServeHTTP(httptest.NewRecorder(), allowed)
	if !called {
		t.Fatal("expected console query token to authenticate")
	}

	called = false
	denied := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me?token="+token, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, denied)
	if called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary query token status=%d called=%v, want unauthorized and not called", rr.Code, called)
	}
}
