package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcsm/api/internal/auth"
)

const rateTestSecret = "rate-test-secret"

// authenticated runs a request through the real auth middleware and hands back
// the request as the rest of the chain would see it — so these tests exercise
// the same context the router builds, not a hand-rolled stand-in.
func authenticated(t *testing.T, authorization string) *http.Request {
	t.Helper()
	keys := func(_ context.Context, presented, ip string) (*auth.MachineIdentity, error) {
		// presented is "mcsm_pat_<key id>" in these tests.
		return &auth.MachineIdentity{
			KeyID:     presented[len(auth.AccessKeyPrefix):],
			UserID:    "user-1",
			Email:     "u@example.com",
			Role:      "user",
			Scopes:    []string{"view"},
			ServerIDs: []string{"srv-1"},
		}, nil
	}
	var captured *http.Request
	handler := auth.Middleware(rateTestSecret, auth.NewTicketStore(), keys)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { captured = r }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	req.Header.Set("Authorization", authorization)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured == nil {
		t.Fatalf("authentication rejected %q", authorization)
	}
	return captured
}

// Rate limiting is keyed by the acting credential, not just the human behind
// it. Two keys owned by the same user must not share a bucket, and neither must
// share the owner's interactive one — otherwise a noisy polling agent throttles
// the person who created it out of their own panel.
func TestRateKeyDistinguishesKeysFromTheirOwner(t *testing.T) {
	jwtTok, err := auth.IssueAccessToken(rateTestSecret, "user-1", "u@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}

	human := rateKey(authenticated(t, "Bearer "+jwtTok))
	bucketA := rateKey(authenticated(t, "Bearer "+auth.AccessKeyPrefix+"key-a"))
	bucketB := rateKey(authenticated(t, "Bearer "+auth.AccessKeyPrefix+"key-b"))

	anonReq := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	anonReq.RemoteAddr = "203.0.113.7:1234"
	anon := rateKey(anonReq)

	if human != "u:user-1" {
		t.Fatalf("human bucket=%q, want the user id", human)
	}
	if bucketA == human || bucketB == human {
		t.Fatalf("a key shares its owner's bucket: %q / %q vs %q", bucketA, bucketB, human)
	}
	if bucketA == bucketB {
		t.Fatalf("two keys owned by one user share a bucket: %q", bucketA)
	}
	if anon != "ip:203.0.113.7" {
		t.Fatalf("unauthenticated bucket=%q, want the client IP", anon)
	}
}

// The bucket itself still behaves: a key that exhausts its allowance is
// throttled, and a second key is unaffected.
func TestRateLimiterIsolatesKeys(t *testing.T) {
	rl := NewRateLimiter(60, 2)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	call := func(keyID string) int {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, authenticated(t, "Bearer "+auth.AccessKeyPrefix+keyID))
		return rr.Code
	}

	// Two tokens of burst, then the third trips.
	if call("noisy") != http.StatusNoContent || call("noisy") != http.StatusNoContent {
		t.Fatal("the first two requests should pass")
	}
	if got := call("noisy"); got != http.StatusTooManyRequests {
		t.Fatalf("third request status=%d want 429", got)
	}
	if got := call("quiet"); got != http.StatusNoContent {
		t.Fatalf("an unrelated key was throttled: status=%d", got)
	}
}
