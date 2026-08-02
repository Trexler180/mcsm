package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert emits a self-signed certificate with the given SANs and returns its
// path. The key is discarded: linkHostForTLS only ever reads the certificate.
func writeCert(t *testing.T, dnsNames []string, ips []net.IP) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	path := filepath.Join(t.TempDir(), "cert.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path
}

// The happy case for a properly-provisioned local cert: an IP SAN for loopback
// means the mod can dial 127.0.0.1 and still verify, so the launch token never
// touches the network.
func TestLinkHostKeepsLoopbackWhenTheCertCoversIt(t *testing.T) {
	cert := writeCert(t, []string{"node.example.com"}, []net.IP{net.ParseIP("127.0.0.1")})

	host, reason := linkHostForTLS(cert, "127.0.0.1")
	if host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", host)
	}
	if reason != "" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

// The bug this whole function exists for. A cert issued for the node's FQDN does
// not cover a loopback literal, so advertising https://127.0.0.1 gives the mod a
// certificate its own hostname verification rejects — and it retries forever.
func TestLinkHostAvoidsUnverifiableLoopback(t *testing.T) {
	cert := writeCert(t, []string{"node.example.com"}, nil)

	host, reason := linkHostForTLS(cert, "127.0.0.1")
	if host == "127.0.0.1" {
		t.Fatal("advertised a loopback literal the certificate does not cover")
	}
	if host != "node.example.com" {
		t.Errorf("host = %q, want node.example.com", host)
	}
	if reason == "" {
		t.Error("leaving loopback should be explained")
	}
}

// "localhost" is preferred over an FQDN when the cert covers it: it verifies and
// keeps the traffic on the loopback interface.
func TestLinkHostPrefersLocalhostOverLeavingLoopback(t *testing.T) {
	cert := writeCert(t, []string{"localhost", "node.example.com"}, nil)

	host, reason := linkHostForTLS(cert, "127.0.0.1")
	if host != "localhost" {
		t.Errorf("host = %q, want localhost", host)
	}
	if reason == "" {
		t.Error("substituting the host should be explained")
	}
}

// A non-loopback bind whose certificate names it is already correct.
func TestLinkHostKeepsANamedNonLoopbackBind(t *testing.T) {
	cert := writeCert(t, []string{"node.example.com"}, nil)

	host, reason := linkHostForTLS(cert, "node.example.com")
	if host != "node.example.com" {
		t.Errorf("host = %q, want node.example.com", host)
	}
	if reason != "" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

// No usable name at all. Returning "" disables the link deliberately: every
// server retrying a handshake that cannot succeed is worse than none trying.
func TestLinkHostDisablesLinkWhenNoNameIsUsable(t *testing.T) {
	cert := writeCert(t, nil, []net.IP{net.ParseIP("10.1.2.3")})

	host, reason := linkHostForTLS(cert, "127.0.0.1")
	if host != "" {
		t.Errorf("host = %q, want \"\" (link disabled)", host)
	}
	if reason == "" {
		t.Error("disabling the link should be explained")
	}
}

// An unreadable certificate must not fail silently, but it also must not disable
// a link that might work — TLS may terminate somewhere we cannot inspect.
func TestLinkHostReportsAnUnreadableCertificate(t *testing.T) {
	host, reason := linkHostForTLS(filepath.Join(t.TempDir(), "missing.pem"), "127.0.0.1")
	if host != "127.0.0.1" {
		t.Errorf("host = %q, want the preferred host", host)
	}
	if reason == "" {
		t.Error("an unreadable certificate should be explained")
	}
}

func TestLinkHostRejectsANonCertificatePEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, reason := linkHostForTLS(path, "127.0.0.1"); reason == "" {
		t.Error("a PEM with no CERTIFICATE block should be reported")
	}
}
