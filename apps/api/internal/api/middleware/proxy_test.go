package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func resolve(t *testing.T, trusted, peer string, headers map[string]string) string {
	t.Helper()
	var got string
	h := TrustedProxy(ParseTrustedProxies(trusted))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIP(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestTrustedProxyRejectsTrueClientIPFromTrustedPeer(t *testing.T) {
	got := resolve(t, "10.0.0.1", "10.0.0.1:4321", map[string]string{
		"True-Client-IP": "203.0.113.99",
		"X-Real-IP":      "198.51.100.42",
	})
	if got != "198.51.100.42" {
		t.Fatalf("client = %q, want X-Real-IP; True-Client-IP must not win", got)
	}
}

func TestTrustedProxyStripsClientIPHeadersFromUntrustedPeer(t *testing.T) {
	got := resolve(t, "10.0.0.1", "192.0.2.25:4321", map[string]string{
		"True-Client-IP":  "203.0.113.99",
		"X-Real-IP":       "198.51.100.42",
		"X-Forwarded-For": "198.51.100.43",
	})
	if got != "192.0.2.25" {
		t.Fatalf("client = %q, want the untrusted network peer", got)
	}
}

// nginx's $proxy_add_x_forwarded_for appends the real peer to whatever the
// client sent. The client controls everything left of that, so a rotating
// fake value must never become the address the login throttle keys on.
func TestTrustedProxyIgnoresSpoofedLeftmostForwardedFor(t *testing.T) {
	got := resolve(t, "", "127.0.0.1:5555", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 203.0.113.7",
	})
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want the address nginx appended, not the spoofed one", got)
	}
}

func TestTrustedProxySkipsTrustedHopsInForwardedFor(t *testing.T) {
	got := resolve(t, "10.0.0.0/8", "10.0.0.2:5555", map[string]string{
		"X-Forwarded-For": "6.6.6.6, 203.0.113.7, 10.0.0.9",
	})
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want the first untrusted hop from the right", got)
	}
}

func TestTrustedProxyStopsAtMalformedHop(t *testing.T) {
	got := resolve(t, "", "127.0.0.1:5555", map[string]string{
		"X-Forwarded-For": "203.0.113.7, not-an-ip",
	})
	if got != "127.0.0.1" {
		t.Fatalf("client = %q, want the proxy itself when the chain is unreadable", got)
	}
}

func resolveWithSecret(t *testing.T, secret, peer string, headers map[string]string) (client string, forwarded http.Header) {
	t.Helper()
	h := TrustedProxyWithSecret(nil, secret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		client = ClientIP(r)
		forwarded = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return client, forwarded
}

// With a proxy secret set, a loopback peer is only the proxy if it says so:
// any other local process (a server's mod, say) is an ordinary client, and the
// address it claims is thrown away.
func TestTrustedProxySecretSeparatesTheProxyFromItsNeighbours(t *testing.T) {
	const secret = "s3cret-value"

	got, hdr := resolveWithSecret(t, secret, "127.0.0.1:5555", map[string]string{
		ProxySecretHeader:   secret,
		"X-Real-IP":         "203.0.113.7",
		"X-Forwarded-Proto": "https",
	})
	if got != "203.0.113.7" {
		t.Fatalf("proxy with the secret: client = %q, want the forwarded address", got)
	}
	if hdr.Get("X-Forwarded-Proto") != "https" {
		t.Fatal("the proxy's X-Forwarded-Proto was dropped")
	}
	if hdr.Get(ProxySecretHeader) != "" {
		t.Fatal("the proxy secret travelled past the middleware")
	}

	for name, headers := range map[string]map[string]string{
		"no secret":    {"X-Real-IP": "203.0.113.7", "X-Forwarded-Proto": "https"},
		"wrong secret": {ProxySecretHeader: "guess", "X-Real-IP": "203.0.113.7", "X-Forwarded-For": "203.0.113.8"},
	} {
		got, hdr := resolveWithSecret(t, secret, "127.0.0.1:5555", headers)
		if got != "127.0.0.1" {
			t.Errorf("%s: client = %q, want the loopback peer itself", name, got)
		}
		for _, h := range []string{"X-Real-IP", "X-Forwarded-For", "X-Forwarded-Proto", ProxySecretHeader} {
			if hdr.Get(h) != "" {
				t.Errorf("%s: %s reached the handler", name, h)
			}
		}
	}
}

// Without a secret configured nothing changes: the header is ignored, and
// still never forwarded.
func TestTrustedProxyWithoutSecretKeepsAddressTrust(t *testing.T) {
	got, hdr := resolveWithSecret(t, "", "127.0.0.1:5555", map[string]string{
		ProxySecretHeader: "anything",
		"X-Real-IP":       "203.0.113.7",
	})
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want the forwarded address", got)
	}
	if hdr.Get(ProxySecretHeader) != "" {
		t.Fatal("the proxy secret header travelled past the middleware")
	}
}
