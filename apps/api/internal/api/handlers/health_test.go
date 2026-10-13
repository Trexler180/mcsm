package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The public health check must not name the build to the internet, but the
// deploy script's direct loopback probe still sees it.
func TestHealthHidesVersionFromProxiedCallers(t *testing.T) {
	Version = "abc123"
	t.Cleanup(func() { Version = "dev" })

	local := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	local.RemoteAddr = "127.0.0.1:5000"
	rr := httptest.NewRecorder()
	Health(rr, local)
	if !strings.Contains(rr.Body.String(), "abc123") {
		t.Fatalf("a direct local probe should see the version: %s", rr.Body.String())
	}

	proxied := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	proxied.RemoteAddr = "127.0.0.1:5000"
	proxied.Header.Set("X-Real-IP", "203.0.113.9")
	rr = httptest.NewRecorder()
	Health(rr, proxied)
	if strings.Contains(rr.Body.String(), "abc123") {
		t.Fatal("a request through the proxy saw the build version")
	}
}
