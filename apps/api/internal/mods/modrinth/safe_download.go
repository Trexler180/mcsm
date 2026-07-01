package modrinth

import (
	"net"
	"syscall"

	"github.com/mcsm/api/internal/safedial"
)

// allowLoopbackDownloads relaxes the SSRF dial guard so tests can download from
// an httptest server (which binds loopback). It must never be set in production;
// it exists only as a test seam, mirroring the package's other test hooks.
var allowLoopbackDownloads = false

// guardedDialControl rejects outbound download connections whose resolved peer
// is not a public address. Download URLs can be attacker-influenced — most
// sharply a crafted .mrpack manifest, whose `downloads` entries are fetched
// verbatim — so without this an install could make the API host reach the cloud
// metadata endpoint (169.254.169.254), the loopback agent, or any internal
// service. Because the check runs on the *connect address* (post-DNS), it also
// defeats DNS rebinding: a public hostname that resolves to 127.0.0.1 is refused
// at dial time, not just at a pre-flight lookup.
//
// It reads the allowLoopbackDownloads seam on each call (the dialer is built once
// at client construction, but the test toggles the flag later), then delegates to
// the shared safedial guard.
func guardedDialControl(network, address string, c syscall.RawConn) error {
	return safedial.Guard{AllowLoopback: allowLoopbackDownloads}.Control(network, address, c)
}

// validateDownloadURL rejects anything that isn't a well-formed http(s) URL with
// a host, so file://, data:, gopher:// and similar can't slip through.
func validateDownloadURL(raw string) error {
	return safedial.ValidateHTTPURL(raw)
}

// isPublicIP is retained as a thin alias over safedial for this package's tests.
func isPublicIP(ip net.IP) bool { return safedial.IsPublicIP(ip) }
