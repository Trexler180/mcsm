package safedial

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":          true,
		"1.1.1.1":          true,
		"127.0.0.1":        false, // loopback
		"10.0.0.5":         false, // private
		"192.168.1.10":     false, // private
		"172.16.0.1":       false, // private
		"169.254.169.254":  false, // link-local (cloud metadata)
		"100.64.0.1":       false, // CGNAT
		"0.0.0.0":          false, // unspecified
		"::1":              false, // loopback v6
		"2606:4700::1111":  true,  // public v6
		"::ffff:127.0.0.1": false, // IPv4-mapped loopback
		"198.18.0.1":       false, // benchmarking
		"240.0.0.1":        false, // reserved
		"255.255.255.255":  false, // broadcast
		"192.0.0.8":        false, // protocol assignments
		"fd00::1":          false, // unique local
		"fec0::1":          false, // site-local
		"::7f00:1":         false, // IPv4-compatible
		// Translators and relays to the IPv4 address inside.
		"64:ff9b::7f00:1":    false, // NAT64 to 127.0.0.1
		"64:ff9b::a9fe:a9fe": false, // NAT64 to 169.254.169.254
		"64:ff9b:1::a00:5":   false, // local NAT64 to 10.0.0.5
		"64:ff9b::808:808":   true,  // NAT64 to 8.8.8.8
		"2002:c0a8:10a::1":   false, // 6to4 for 192.168.1.10
		"2002:808:808::1":    true,  // 6to4 for 8.8.8.8
	}
	for ip, want := range cases {
		if got := IsPublicIP(net.ParseIP(ip)); got != want {
			t.Errorf("IsPublicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestValidateHTTPURL(t *testing.T) {
	bad := []string{"file:///etc/passwd", "gopher://x", "ftp://h/x", "notaurl", "https://"}
	for _, u := range bad {
		if err := ValidateHTTPURL(u); err == nil {
			t.Errorf("ValidateHTTPURL(%q) = nil, want error", u)
		}
	}
	if err := ValidateHTTPURL("https://example.com/hook"); err != nil {
		t.Errorf("valid url rejected: %v", err)
	}
}

func TestClientBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Default guard must refuse to dial loopback (SSRF protection).
	if _, err := Client(Guard{}, 3*time.Second).Get(srv.URL); err == nil {
		t.Fatal("expected loopback connection to be refused, got nil error")
	}

	// With the test seam it connects.
	resp, err := Client(Guard{AllowLoopback: true}, 3*time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("AllowLoopback client should connect: %v", err)
	}
	resp.Body.Close()
}
