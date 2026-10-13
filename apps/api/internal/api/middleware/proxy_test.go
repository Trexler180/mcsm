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
