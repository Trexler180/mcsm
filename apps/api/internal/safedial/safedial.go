// Package safedial provides SSRF protection for outbound HTTP requests whose
// target URL is attacker-influenced — crafted mod-download manifests and
// user-configured webhook URLs alike. The guard runs on the *resolved connect
// address* (post-DNS), so it also defeats DNS rebinding: a public hostname that
// resolves to 127.0.0.1 is refused at dial time, not just at a pre-flight
// lookup.
package safedial

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// Guard rejects connections to non-public addresses. AllowLoopback relaxes the
// check for tests that talk to an httptest server on loopback; it must never be
// set in production.
type Guard struct {
	AllowLoopback bool
}

// Control is a net.Dialer.Control hook. It inspects the address the dialer is
// about to connect to (already resolved to an IP) and refuses anything that
// isn't globally routable.
func (g Guard) Control(_, address string, _ syscall.RawConn) error {
	if g.AllowLoopback {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("safedial: malformed dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("safedial: unresolved dial host %q", host)
	}
	if !IsPublicIP(ip) {
		return fmt.Errorf("safedial: refusing to connect to non-public address %s", ip)
	}
	return nil
}

// IsPublicIP reports whether ip is a globally routable unicast address, i.e. not
// loopback, private, link-local, multicast, unspecified, CGNAT, or 0.0.0.0/8.
func IsPublicIP(ip net.IP) bool {
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

// ValidateHTTPURL rejects anything that isn't a well-formed http(s) URL with a
// host, so file://, data:, gopher:// and similar can't slip through.
func ValidateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must be http(s), got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("url has no host")
	}
	return nil
}

// Client returns an *http.Client whose dialer refuses non-public peers (including
// across redirects, since the dial guard runs for every hop) and which caps the
// redirect chain. timeout bounds the whole request.
func Client(g Guard, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: g.Control}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("safedial: stopped after 5 redirects")
			}
			return nil
		},
	}
}
