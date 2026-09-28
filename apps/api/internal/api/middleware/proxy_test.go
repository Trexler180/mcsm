package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestTrustedProxyRejectsTrueClientIPFromTrustedPeer(t *testing.T) {
	var got string
	h := TrustedProxy(ParseTrustedProxies("10.0.0.1"))(chimw.RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:4321"
	req.Header.Set("True-Client-IP", "203.0.113.99")
	req.Header.Set("X-Real-IP", "198.51.100.42")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "198.51.100.42" {
		t.Fatalf("RemoteAddr = %q, want authenticated X-Real-IP; True-Client-IP must not win", got)
	}
}

func TestTrustedProxyStripsClientIPHeadersFromUntrustedPeer(t *testing.T) {
	var got string
	h := TrustedProxy(ParseTrustedProxies("10.0.0.1"))(chimw.RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.25:4321"
	req.Header.Set("True-Client-IP", "203.0.113.99")
	req.Header.Set("X-Real-IP", "198.51.100.42")
	req.Header.Set("X-Forwarded-For", "198.51.100.43")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "192.0.2.25:4321" {
		t.Fatalf("RemoteAddr = %q, want untrusted network peer", got)
	}
}
