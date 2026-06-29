package modrinth

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIsPublicIPRejectsInternalRanges(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"127.0.0.1":       false,
		"10.1.2.3":        false,
		"192.168.0.10":    false,
		"172.16.5.5":      false,
		"169.254.169.254": false, // cloud metadata
		"100.64.0.1":      false, // CGNAT
		"0.0.0.0":         false,
		"::1":             false,
		"fe80::1":         false,
		"fc00::1":         false,
		"2606:4700:4700::1111": true,
	}
	for ip, wantPublic := range cases {
		if got := isPublicIP(parseIPOrFail(t, ip)); got != wantPublic {
			t.Errorf("isPublicIP(%s) = %v, want %v", ip, got, wantPublic)
		}
	}
}

func TestValidateDownloadURLRejectsNonHTTP(t *testing.T) {
	bad := []string{"file:///etc/passwd", "data:text/plain,hi", "ftp://host/x", "gopher://h", "://nohost", "https://"}
	for _, u := range bad {
		if err := validateDownloadURL(u); err == nil {
			t.Errorf("expected %q to be rejected", u)
		}
	}
	if err := validateDownloadURL("https://cdn.modrinth.com/x.jar"); err != nil {
		t.Errorf("valid url rejected: %v", err)
	}
}

func TestDownloadBlocksLoopbackByDefault(t *testing.T) {
	// Guard active (default): a download targeting loopback must be refused at dial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	_, err := New().Download(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("expected loopback download to be blocked by the SSRF guard")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("expected non-public refusal, got: %v", err)
	}
}

func TestDownloadVerifiedSHA512AndSizeCap(t *testing.T) {
	allowLoopbackDownloads = true
	defer func() { allowLoopbackDownloads = false }()

	payload := []byte("modpack-file-bytes")
	sum := sha512.Sum512(payload)
	good512 := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	// Correct sha512 within the cap → success.
	path, err := New().DownloadVerified(context.Background(), srv.URL, "", good512, 1<<20)
	if err != nil {
		t.Fatalf("valid sha512 download failed: %v", err)
	}
	os.Remove(path)

	// Wrong sha512 → rejected.
	if _, err := New().DownloadVerified(context.Background(), srv.URL, "", "deadbeef", 0); err == nil {
		t.Error("expected sha512 mismatch error")
	}

	// Body larger than the cap → rejected.
	if _, err := New().DownloadVerified(context.Background(), srv.URL, "", "", 4); err == nil {
		t.Error("expected size-limit error")
	}
}

func parseIPOrFail(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test IP %q", s)
	}
	return ip
}
