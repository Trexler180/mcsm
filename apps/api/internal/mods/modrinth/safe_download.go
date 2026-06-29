package modrinth

import (
	"fmt"
	"net"
	"net/url"
	"syscall"
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
func guardedDialControl(network, address string, _ syscall.RawConn) error {
	if allowLoopbackDownloads {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("download: malformed dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("download: unresolved dial host %q", host)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("download: refusing to connect to non-public address %s", ip)
	}
	return nil
}

// isPublicIP reports whether ip is a globally routable unicast address, i.e. not
// loopback, private, link-local, multicast, unspecified, CGNAT, or 0.0.0.0/8.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// IsPrivate misses these IPv4 ranges that are still non-public.
		if v4[0] == 0 { // 0.0.0.0/8 "this host"
			return false
		}
		if v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10 carrier-grade NAT
			return false
		}
	}
	return true
}

// validateDownloadURL rejects anything that isn't a well-formed http(s) URL with
// a host, so file://, data:, gopher:// and similar can't slip through.
func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid download url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("download url must be http(s), got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("download url has no host")
	}
	return nil
}
