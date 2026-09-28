package publicurl

import (
	"net/http/httptest"
	"testing"
)

func spaConfig(base string) Config {
	return Config{PublicOrigin: "http://localhost:3000", BasePath: base}
}

// A base path that is really a filesystem path must never reach the URL.
//
// MSYS2 shells rewrite a lone "/" in an environment variable into the MSYS
// install root before handing it to a native binary, so `make dev-mcp` on
// Windows delivered APP_BASE_PATH="C:/Program Files/Git/". Concatenated, that
// produced http://localhost:3000/C:/Program%20Files/Git/mcp-consent — a 404 at
// the exact moment someone is trying to approve an agent.
func TestMangledBasePathFallsBackToRoot(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost:3000/", nil)
	mangled := []string{
		"C:/Program Files/Git/",
		"/C:/Program Files/Git/",
		`C:\Program Files\Git\`,
		`\dashboard\`,
		"http://localhost:3000/dashboard/",
	}
	for _, base := range mangled {
		got, err := spaConfig(base).SPAURL(r, "mcp-consent")
		if err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
		if want := "http://localhost:3000/mcp-consent"; got != want {
			t.Errorf("base %q produced %q, want the root fallback %q", base, got, want)
		}
	}
}

// The legitimate values still behave, including the subpath production uses.
func TestBasePathNormalization(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost:3000/", nil)
	cases := map[string]string{
		"":            "http://localhost:3000/mcp-consent",
		"/":           "http://localhost:3000/mcp-consent",
		"/dashboard/": "http://localhost:3000/dashboard/mcp-consent",
		"/dashboard":  "http://localhost:3000/dashboard/mcp-consent",
		"dashboard":   "http://localhost:3000/dashboard/mcp-consent",
		"dashboard/":  "http://localhost:3000/dashboard/mcp-consent",
		"  /panel/  ": "http://localhost:3000/panel/mcp-consent",
	}
	for base, want := range cases {
		got, err := spaConfig(base).SPAURL(r, "mcp-consent")
		if err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
		if got != want {
			t.Errorf("base %q produced %q, want %q", base, got, want)
		}
	}
}

// A leading slash on the route must not double up against the base path.
func TestSPARouteLeadingSlashIsAbsorbed(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost:3000/", nil)
	got, err := spaConfig("/dashboard/").SPAURL(r, "/mcp-consent")
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://localhost:3000/dashboard/mcp-consent"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── Origin resolution ────────────────────────────────────────────

// The loopback fallback exists so local development needs no configuration. It
// must key on where the request actually came from, not only on what it claimed
// to be talking to: a reverse proxy that rewrites Host to 127.0.0.1 would
// otherwise hand a public deployment a loopback origin and bind real tokens to
// a cleartext audience.
func TestLoopbackFallbackRequiresALocalPeer(t *testing.T) {
	var unconfigured Config
	r := httptest.NewRequest("GET", "http://127.0.0.1:3000/api/v1/mcp", nil)
	r.RemoteAddr = "203.0.113.7:41234" // a real remote client

	if origin, err := unconfigured.Origin(r); err == nil {
		t.Fatalf("a remote peer claiming a loopback Host resolved an origin: %q", origin)
	}
}

func TestLoopbackFallbackResolvesForALocalPeer(t *testing.T) {
	var unconfigured Config
	r := httptest.NewRequest("GET", "http://127.0.0.1:3000/api/v1/mcp", nil)
	r.RemoteAddr = "127.0.0.1:41234"

	origin, err := unconfigured.Origin(r)
	if err != nil {
		t.Fatalf("loopback request did not resolve an origin: %v", err)
	}
	if origin != "http://127.0.0.1:3000" {
		t.Fatalf("origin = %q", origin)
	}
}

// A same-host proxy that forwards the client's Host but not its address leaves
// RemoteAddr at loopback. Any forwarding header marks the request as proxied,
// and a proxied request is not a local one — otherwise an internet client could
// pick the origin by sending `Host: localhost`.
func TestLoopbackFallbackRefusesAProxiedRequest(t *testing.T) {
	var unconfigured Config
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		r := httptest.NewRequest("GET", "http://localhost:3000/api/v1/mcp", nil)
		r.RemoteAddr = "127.0.0.1:41234"
		r.Header.Set(header, "https")
		if origin, err := unconfigured.Origin(r); err == nil {
			t.Errorf("a request carrying %s resolved a loopback origin: %q", header, origin)
		}
	}
}

// A configured origin is authoritative and never consults the request, so a
// remote peer is irrelevant to it.
func TestConfiguredOriginIgnoresThePeer(t *testing.T) {
	c := Config{PublicOrigin: "https://mc.example.com"}
	r := httptest.NewRequest("GET", "https://mc.example.com/api/v1/mcp", nil)
	r.RemoteAddr = "203.0.113.7:41234"

	origin, err := c.Origin(r)
	if err != nil || origin != "https://mc.example.com" {
		t.Fatalf("origin = %q, err = %v", origin, err)
	}
}

// ── Resource comparison ──────────────────────────────────────────

// Scheme and host are case-insensitive per RFC 3986, so a client that spells
// them differently is naming the same resource and must not be refused.
func TestSameResourceFoldsSchemeAndHost(t *testing.T) {
	expected := "https://mc.example.com" + MCPResourcePath
	for _, candidate := range []string{
		"https://mc.example.com/api/v1/mcp",
		"HTTPS://mc.example.com/api/v1/mcp",
		"https://MC.Example.COM/api/v1/mcp",
	} {
		if !SameResource(candidate, expected) {
			t.Errorf("%q should name the same resource as %q", candidate, expected)
		}
	}
}

// The path is where the leniency stops: a different host, port, or path is a
// different resource, and the audience binding depends on saying so.
func TestSameResourceStillSeparatesDifferentResources(t *testing.T) {
	expected := "https://mc.example.com" + MCPResourcePath
	for _, candidate := range []string{
		"https://mc.example.com/api/v1/MCP",
		"https://mc.example.com/api/v1/mcp/other",
		"https://evil.example.com/api/v1/mcp",
		"https://mc.example.com:8443/api/v1/mcp",
		"http://mc.example.com/api/v1/mcp",
		"::not a url::",
	} {
		if SameResource(candidate, expected) {
			t.Errorf("%q must not name the same resource as %q", candidate, expected)
		}
	}
}
