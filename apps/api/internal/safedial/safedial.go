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
		switch {
		case v4[0] == 0: // 0.0.0.0/8 "this host"
			return false
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64.0.0/10 carrier-grade NAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24 protocol assignments
			return false
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18.0.0/15 benchmarking
			return false
		case v4[0] >= 240: // 240.0.0.0/4 reserved, and broadcast
			return false
		}
		return true
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return false
	}
	// Addresses that carry an IPv4 address inside them reach it through a
	// translator or relay: judge them by the address they carry.
	switch {
	case isPrefix(ip16, nat64, 96), isPrefix(ip16, nat64Local, 48):
		return IsPublicIP(net.IP(ip16[12:16]))
	case ip16[0] == 0x20 && ip16[1] == 0x02: // 2002::/16 6to4
		return IsPublicIP(net.IP(ip16[2:6]))
	case ip16[0] == 0xfe && ip16[1]&0xc0 == 0xc0: // fec0::/10 site-local
		return false
	case isPrefix(ip16, make(net.IP, 16), 96): // ::a.b.c.d, IPv4-compatible
		return false
	}
	return true
}

var (
	nat64      = net.ParseIP("64:ff9b::")   // 64:ff9b::/96 well-known NAT64
	nat64Local = net.ParseIP("64:ff9b:1::") // 64:ff9b:1::/48 local-use NAT64
)

// isPrefix reports whether ip (16 bytes) starts with the first bits of prefix.
func isPrefix(ip, prefix net.IP, bits int) bool {
	return net.IP.Mask(ip, net.CIDRMask(bits, 128)).Equal(net.IP.Mask(prefix, net.CIDRMask(bits, 128)))
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
