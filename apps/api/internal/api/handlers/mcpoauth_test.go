package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/mcpserver"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// The 90-day cap is checked on the requested day count, not on the duration it
// produces. Multiplying first overflows time.Duration, and a large enough
// request wrapped back inside the permitted range — so an absurd value became a
// silently short-lived grant instead of the error the caller should have seen.
func TestGrantExpiryRejectsOutOfRangeDayCounts(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	maxDays := int(store.MaxMCPGrantLifetime / (24 * time.Hour))

	if _, err := grantExpiry(maxDays+1, now); err == nil {
		t.Error("a day count just past the cap was accepted")
	}
	// 213504 days is the value that used to wrap to roughly 25 minutes.
	for _, days := range []int{213504, 216000, 1 << 40} {
		if _, err := grantExpiry(days, now); err == nil {
			t.Errorf("days=%d was accepted", days)
		}
	}
}

// The ordinary cases still behave, including the omitted-value default.
func TestGrantExpiryHonoursTheSupportedRange(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

	got, err := grantExpiry(0, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(store.DefaultMCPGrantLifetime).UTC(); !got.Equal(want) {
		t.Errorf("omitted day count = %v, want the default %v", got, want)
	}

	maxDays := int(store.MaxMCPGrantLifetime / (24 * time.Hour))
	got, err = grantExpiry(maxDays, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(store.MaxMCPGrantLifetime).UTC(); !got.Equal(want) {
		t.Errorf("maximum day count = %v, want %v", got, want)
	}
}

// Dynamic registration is open to the internet, so a self-registered client
// must call back to the machine the operator is sitting at. Without this, anyone
// could register a client pointing at a server they control, hand the resulting
// /authorize link to an operator, and collect the code the moment it was
// approved.
func TestSelfRegisteredClientsMustCallBackToLoopback(t *testing.T) {
	remote := []string{"https://attacker.example/callback"}
	if _, err := validateRedirectURIs(remote, false); err == nil {
		t.Fatal("a remote https callback was accepted for a self-registered client")
	}
	if _, err := validateRedirectURIs(remote, true); err != nil {
		t.Fatalf("an operator who opted in was still refused: %v", err)
	}

	// Every client this feature targets calls back to loopback, so the
	// restriction has to leave all of those working.
	loopback := []string{
		"http://127.0.0.1:9876/callback",
		"http://localhost:33418/oauth",
		"http://[::1]:5000/cb",
	}
	for _, uri := range loopback {
		if _, err := validateRedirectURIs([]string{uri}, false); err != nil {
			t.Errorf("loopback callback %q was refused: %v", uri, err)
		}
	}
}

// The pre-existing shape rules are unchanged by the loopback restriction.
func TestRedirectURIShapeRulesStillApply(t *testing.T) {
	cases := map[string]string{
		"a wildcard":            "http://127.0.0.1:*/cb",
		"a fragment":            "http://127.0.0.1:9876/cb#frag",
		"embedded credentials":  "http://user:pw@127.0.0.1:9876/cb",
		"a non-http scheme":     "ftp://127.0.0.1/cb",
		"a relative URI":        "/callback",
		"cleartext off-machine": "http://example.com/cb",
	}
	for name, uri := range cases {
		if _, err := validateRedirectURIs([]string{uri}, true); err == nil {
			t.Errorf("%s was accepted: %q", name, uri)
		}
	}
	if _, err := validateRedirectURIs(nil, true); err == nil {
		t.Error("a client with no redirect URI was accepted")
	}
}

// Every route behind humanCaller decides who may hold or approve a delegation,
// so it refuses both machine credential families rather than only access keys.
//
// An MCP grant does not arrive as an access key. It reaches application code as
// a delegated actor and resolves, through currentUserID, to the human who owns
// it — so a guard that only looked for access keys would read an agent's
// request as that human and let a credential approve its own successor. That is
// the one thing the consent model must never allow.
func TestHumanCallerRefusesADelegatedMCPActor(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/mcp/grants", nil)
	r = r.WithContext(auth.WithDelegatedActor(r.Context(), &auth.DelegatedActor{
		UserID: "user-1", GrantID: "grant-1", ClientName: "Claude Code",
	}))
	w := httptest.NewRecorder()

	if uid := humanCaller(w, r); uid != "" {
		t.Fatalf("a delegated MCP actor resolved to the human %q", uid)
	}
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "interactive sign-in") {
		t.Fatalf("body = %q", w.Body.String())
	}
	// The delegated actor still resolves to its owner for *audit* purposes;
	// that is what makes the guard above load-bearing rather than incidental.
	if got := currentUserID(r); got != "user-1" {
		t.Fatalf("currentUserID = %q, want the grant owner", got)
	}
}

// Registration is where a stranger names themselves, and the consent screen is
// where a human reads that name and decides. Bidi overrides and zero-width
// characters are invisible to that human and fully legible to everything else,
// so they must not survive the round trip — a name that renders as "Claude
// Code" while spelling something else is exactly the claim this screen exists
// to let someone evaluate.
func TestSelfRegisteredNamesAreStrippedOfInvisibleCharacters(t *testing.T) {
	for _, raw := range []string{
		"Claude\u202e Code",
		"Claude\u200b\u200bCode",
		"Claude\u0000Code",
		"Claude\U000e0041Code",
		"Claude\x1b[31m Code",
	} {
		cleaned := mcpserver.CleanUntrusted(raw)
		for _, invisible := range []string{"\u202e", "\u200b", "\u0000", "\U000e0041", "\x1b"} {
			if strings.Contains(cleaned, invisible) {
				t.Errorf("cleaning %q left %q behind: %q", raw, invisible, cleaned)
			}
		}
		if !strings.Contains(cleaned, "Claude") {
			t.Errorf("cleaning %q destroyed the readable name: %q", raw, cleaned)
		}
	}
}

// The resource parameter names the endpoint a token will be bound to. Scheme
// and host are case-insensitive per RFC 3986, so a client spelling them
// differently is naming the same resource — and what gets stored is the
// server's own spelling either way, never the client's.
func TestResourceMatchingFoldsCaseButStoresOurSpelling(t *testing.T) {
	h := &MCPOAuthHandlers{urls: publicurl.Config{PublicOrigin: "https://mc.example.com"}}
	expected := "https://mc.example.com" + publicurl.MCPResourcePath

	for _, raw := range []string{
		"https://mc.example.com/api/v1/mcp",
		"https://MC.Example.com/api/v1/mcp",
		"HTTPS://mc.example.com/api/v1/mcp",
		"https://mc.example.com/api/v1/mcp/",
	} {
		got, err := h.canonicalResource(nil, raw, "https://mc.example.com")
		if err != nil {
			t.Errorf("resource %q was refused: %v", raw, err)
			continue
		}
		if got != expected {
			t.Errorf("resource %q stored as %q, want %q", raw, got, expected)
		}
	}

	for _, raw := range []string{
		"", "https://evil.example.com/api/v1/mcp", "http://mc.example.com/api/v1/mcp",
		"https://mc.example.com/api/v1/other", "https://mc.example.com:8443/api/v1/mcp",
	} {
		if _, err := h.canonicalResource(nil, raw, "https://mc.example.com"); err == nil {
			t.Errorf("resource %q was accepted", raw)
		}
	}
}

// A public client authenticates to the revocation endpoint by naming itself.
// Without client_id the store could not tell whose delegation a presented
// refresh token belongs to, so the request is malformed — and saying so, rather
// than answering 200, tells the client its tokens are still live.
func TestRevokeRequiresClientID(t *testing.T) {
	h := &MCPOAuthHandlers{store: authTestStore(t)}

	missing := httptest.NewRequest("POST", "/api/v1/oauth/revoke",
		strings.NewReader("token="+store.MCPRefreshPrefix+"whatever"))
	missing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Revoke(w, missing)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") {
		t.Fatalf("missing client_id: status=%d body=%q", w.Code, w.Body.String())
	}

	// With a client_id, an unknown token is still a silent 200: no oracle.
	named := httptest.NewRequest("POST", "/api/v1/oauth/revoke",
		strings.NewReader("client_id=mcp_client_x&token="+store.MCPRefreshPrefix+"whatever"))
	named.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.Revoke(w, named)
	if w.Code != 200 {
		t.Fatalf("named client, unknown token: status=%d body=%q", w.Code, w.Body.String())
	}
}
