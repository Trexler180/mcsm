package auth

import (
	"context"
	"errors"
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
	handler := Middleware(secret, tickets, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// A machine access key authenticates through the same Authorization header,
// but only the reserved prefix reaches key lookup — everything else stays on
// the JWT path, so the two credential families can never be confused.
func TestAccessKeyBearerRouting(t *testing.T) {
	secret := "secret"
	const goodToken = AccessKeyPrefix + "good-secret"

	var lookups []string
	keys := func(_ context.Context, presented, ip string) (*MachineIdentity, error) {
		lookups = append(lookups, presented)
		if presented != goodToken {
			return nil, errors.New("invalid access key")
		}
		return &MachineIdentity{
			KeyID: "key-1", UserID: "user-1", Email: "u@example.com", Role: "user",
			Scopes: []string{"view"}, ServerIDs: []string{"srv-1"},
		}, nil
	}

	var gotClaims *Claims
	var gotMachine *MachinePrincipal
	handler := Middleware(secret, NewTicketStore(), keys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFrom(r.Context())
		gotMachine = MachineFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func(header string) *httptest.ResponseRecorder {
		gotClaims, gotMachine = nil, nil
		req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// A valid key produces both owner claims and a separate machine principal.
	if rr := do("Bearer " + goodToken); rr.Code != http.StatusNoContent {
		t.Fatalf("valid key status=%d want 204", rr.Code)
	}
	if gotClaims == nil || gotClaims.UserID != "user-1" || gotClaims.Role != "user" {
		t.Fatalf("claims=%+v want the key owner", gotClaims)
	}
	if gotMachine == nil || gotMachine.KeyID != "key-1" {
		t.Fatalf("machine=%+v want the key principal", gotMachine)
	}
	if !gotMachine.AllowsServer("srv-1") || gotMachine.AllowsServer("srv-2") || gotMachine.AllowsServer("") {
		t.Fatal("the allowlist must permit exactly the listed servers")
	}

	// Every rejected key state is one uniform 401.
	for _, header := range []string{
		"Bearer " + AccessKeyPrefix + "wrong",
		"Bearer " + AccessKeyPrefix,
		"Bearer mcsm_pat", // near-miss prefix: parsed as a JWT, still rejected
	} {
		rr := do(header)
		if rr.Code != http.StatusUnauthorized || gotMachine != nil {
			t.Fatalf("header %q status=%d machine=%v, want 401 and no principal", header, rr.Code, gotMachine)
		}
	}

	// A JWT still authenticates as a human, with no machine principal, and never
	// reaches the key lookup.
	before := len(lookups)
	jwtTok, err := IssueAccessToken(secret, "user-2", "h@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if rr := do("Bearer " + jwtTok); rr.Code != http.StatusNoContent {
		t.Fatalf("JWT status=%d want 204", rr.Code)
	}
	if gotMachine != nil {
		t.Fatal("a JWT must not produce a machine principal")
	}
	if len(lookups) != before {
		t.Fatalf("a JWT reached the access-key lookup: %v", lookups[before:])
	}

	// A machine key is never accepted from the query string, even on the
	// endpoints that accept browser tickets.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/srv-1/console?ticket="+goodToken, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("key as ticket status=%d want 401", rr.Code)
	}
}

// With no key lookup wired in, machine authentication is simply off: the
// migration can land before the feature is switched on.
func TestAccessKeysRejectedWhenLookupIsAbsent(t *testing.T) {
	handler := Middleware("secret", NewTicketStore(), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+AccessKeyPrefix+"anything")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rr.Code)
	}
}

// AdminOnly trusts the token's role claim, which a key populates from its
// owner. It must still refuse machine principals outright.
func TestAdminOnlyRefusesMachinePrincipals(t *testing.T) {
	keys := func(_ context.Context, presented, ip string) (*MachineIdentity, error) {
		return &MachineIdentity{
			KeyID: "key-1", UserID: "admin-1", Email: "a@example.com", Role: "admin",
			Scopes: []string{"view"}, ServerIDs: []string{"srv-1"},
		}, nil
	}
	handler := Middleware("secret", NewTicketStore(), keys)(AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("an access key must never reach an admin-only handler")
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+AccessKeyPrefix+"admin-owned")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rr.Code)
	}
}
