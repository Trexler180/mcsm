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
	"True-Client-IP",
}

// TrustedProxy establishes the caller's address. It is the only place that
// reads forwarding headers, and it must run before anything that looks at
// r.RemoteAddr (rate limiting, audit, login throttling).
//
// From a peer that is not a trusted proxy, every forwarding header is removed
// and the TCP peer is the client.
//
// From a trusted proxy, the client is taken from X-Real-IP when present (our
// nginx config sets it to $remote_addr, a value the client cannot influence),
// otherwise from X-Forwarded-For read right to left, skipping trusted proxies.
// Reading from the right matters: nginx's $proxy_add_x_forwarded_for appends
// the real address to whatever the client sent, so the leftmost entry is
// attacker-chosen. Taking it is what let a rotating fake header give every
// login attempt its own throttle bucket.
//
// When trusted is empty it defaults to loopback only, matching the supported
// "reverse proxy on the same host" production shape; operators terminating TLS
// on a separate host set TRUSTED_PROXIES to that proxy's CIDR.
func TrustedProxy(trusted []*net.IPNet) func(http.Handler) http.Handler {
	if len(trusted) == 0 {
		trusted = loopbackNets()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// True-Client-IP is not part of our proxy contract; never honor it.
			r.Header.Del("True-Client-IP")
			if !peerTrusted(r.RemoteAddr, trusted) {
				for _, h := range forwardedHeaders {
					r.Header.Del(h)
				}
				next.ServeHTTP(w, r)
				return
			}
			if ip := forwardedClient(r, trusted); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

// forwardedClient resolves the client address a trusted proxy reported, or ""
// when it reported nothing usable.
func forwardedClient(r *http.Request, trusted []*net.IPNet) string {
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	var hops []string
	for _, value := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				hops = append(hops, part)
			}
		}
	}
	// Right to left: the nearest hop our proxy vouches for comes last.
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			// A malformed entry is where the trustworthy part of the chain
			// ends; nothing to its left can be believed either.
			return ""
		}
		if !ipTrusted(ip, trusted) {
			return ip.String()
		}
	}
	return ""
}

// ClientIP is the caller's address as established by TrustedProxy. Every
// consumer — throttles, audit, session records — reads it through here rather
// than from headers, so there is exactly one answer to "who is calling".
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func peerTrusted(remoteAddr string, trusted []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ipTrusted(ip, trusted)
}

func ipTrusted(ip net.IP, trusted []*net.IPNet) bool {
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
