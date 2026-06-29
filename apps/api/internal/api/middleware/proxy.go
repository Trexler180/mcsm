package middleware

import (
	"net"
	"net/http"
	"strings"
)

// forwardedHeaders are the client-controllable hop headers we only trust from a
// known proxy. Left unstripped, a direct caller could spoof its source IP (to
// poison audit logs or bypass IP-based throttling) or claim X-Forwarded-Proto:
// https to flip the "Secure" cookie flag on a plain-HTTP connection.
var forwardedHeaders = []string{
	"X-Forwarded-For",
	"X-Forwarded-Proto",
	"X-Forwarded-Host",
	"X-Real-IP",
	"Forwarded",
}

// TrustedProxy strips forwarded headers from requests whose immediate peer is
// not in trusted. Run this BEFORE chi's RealIP and any handler that reads
// X-Forwarded-*. When trusted is empty it defaults to loopback only, matching
// the supported "reverse proxy on the same host" production shape; operators
// terminating TLS on a separate host set TRUSTED_PROXIES to that proxy's CIDR.
func TrustedProxy(trusted []*net.IPNet) func(http.Handler) http.Handler {
	if len(trusted) == 0 {
		trusted = loopbackNets()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !peerTrusted(r.RemoteAddr, trusted) {
				for _, h := range forwardedHeaders {
					r.Header.Del(h)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func peerTrusted(remoteAddr string, trusted []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies parses a comma-separated list of IPs and/or CIDRs (e.g.
// "10.0.0.0/8, 192.168.1.5") into networks. Bare IPs become /32 or /128.
// Invalid entries are skipped. An empty/whitespace input yields nil, which
// TrustedProxy treats as "loopback only".
func ParseTrustedProxies(raw string) []*net.IPNet {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			out = append(out, n)
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return out
}

func loopbackNets() []*net.IPNet {
	_, v4, _ := net.ParseCIDR("127.0.0.0/8")
	_, v6, _ := net.ParseCIDR("::1/128")
	return []*net.IPNet{v4, v6}
}
