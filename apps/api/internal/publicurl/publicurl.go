// Package publicurl resolves the deployment's canonical public origin and the
// absolute URLs built from it.
//
// It exists because OAuth and MCP are the first features whose correctness
// depends on the server knowing its own external address. A token is bound to
// the exact resource URL it was issued for, and discovery documents advertise
// absolute endpoints, so "whatever host this request claimed" is not an
// acceptable answer: an attacker who can set a Host header could otherwise
// steer a client at a resource identifier of their choosing.
//
// The resolution order is therefore configuration first, request second, and
// the request is trusted only when it is unambiguously local.
package publicurl

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ErrNoOrigin means the deployment has not been told its public origin and the
// request did not come from loopback. Callers must fail closed: guessing here
// would mint credentials bound to an attacker-chosen audience.
var ErrNoOrigin = errors.New("no public origin configured")

// Config is the resolved environment. Zero values mean "unset", which is the
// normal state in local development.
type Config struct {
	// PublicOrigin is MCP_PUBLIC_ORIGIN: an explicit origin for the MCP and
	// OAuth surface, for deployments where it differs from the panel's own.
	PublicOrigin string
	// AppOrigin is APP_ORIGIN, which production already sets for the WebSocket
	// allowlist and outbound links.
	AppOrigin string
	// BasePath is APP_BASE_PATH, the path the SPA is served under. Production
	// serves the dashboard below /dashboard/; local dev serves it at /.
	BasePath string
	// SPAOrigin is APP_SPA_ORIGIN, needed only where the dashboard is served
	// from a different origin than the API — which in practice means local
	// development, where Vite has its own port. In production the two are the
	// same origin and this stays unset.
	SPAOrigin string
}

func FromEnv() Config {
	return Config{
		PublicOrigin: strings.TrimSpace(os.Getenv("MCP_PUBLIC_ORIGIN")),
		AppOrigin:    strings.TrimSpace(os.Getenv("APP_ORIGIN")),
		BasePath:     strings.TrimSpace(os.Getenv("APP_BASE_PATH")),
		SPAOrigin:    strings.TrimSpace(os.Getenv("APP_SPA_ORIGIN")),
	}
}

// Origin returns the canonical scheme://host[:port] for this deployment.
//
// Order:
//
//  1. MCP_PUBLIC_ORIGIN — set deliberately for this surface.
//  2. APP_ORIGIN — the value production already sets.
//  3. The request's own host, but only when the request is local — both the
//     address it arrived from and the host it named.
//
// Step 3 is what keeps `make dev-api` and the local demo working with no
// configuration at all. Both halves of the check are needed for it to be safe.
// The Host header alone is not evidence of anything: a reverse proxy that
// rewrites Host to 127.0.0.1 — a common enough upstream configuration — would
// otherwise hand a public deployment a loopback origin, binding real tokens to
// a cleartext audience nobody could reach. RemoteAddr is what says the request
// came from this machine, and it is safe to read because TrustedProxy has
// already stripped forwarded headers from any peer that is not trusted.
//
// A request carrying any forwarding header is refused too, even from loopback.
// Forwarding headers mean a proxy handled it, and a proxied request is not a
// local one: a same-host proxy that passes the client's Host through but does
// not forward the client's address would otherwise leave RemoteAddr at
// 127.0.0.1 and let anyone on the internet choose a loopback origin by sending
// `Host: localhost`. The dev proxy (Vite) adds none of these, so local
// development is unaffected.
//
// A deployment that trips this fails closed with ErrNoOrigin and a log line
// naming the variable to set, which is a better outcome than a working feature
// built on a guess.
//
// For it to name the port the client is really talking to, the dev proxy must
// forward the browser's Host header unchanged — see apps/web/vite.config.ts.
func (c Config) Origin(r *http.Request) (string, error) {
	for _, configured := range []string{c.PublicOrigin, c.AppOrigin} {
		if configured == "" {
			continue
		}
		origin, err := NormalizeOrigin(configured)
		if err != nil {
			return "", err
		}
		return origin, nil
	}
	if r == nil {
		return "", ErrNoOrigin
	}
	if !IsLoopbackHost(r.RemoteAddr) || proxied(r) {
		return "", ErrNoOrigin
	}
	host := requestHost(r)
	if host == "" || !IsLoopbackHost(host) {
		return "", ErrNoOrigin
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host, nil
}

// forwardingHeaders are the headers any reverse proxy adds. Their presence is
// what distinguishes a proxied request from one made on this machine.
var forwardingHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-IP",
}

func proxied(r *http.Request) bool {
	for _, h := range forwardingHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// NormalizeOrigin validates a configured origin and strips anything that is not
// scheme and authority. An origin carrying a path would silently produce
// resource identifiers that do not match what a client canonicalizes, which
// shows up as an unexplainable audience mismatch rather than an error.
func NormalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("public origin must be an absolute URL")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("public origin must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("public origin must not include a query, fragment, or credentials")
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Bearer tokens over cleartext are only tolerable when the traffic never
		// leaves the machine.
		if !IsLoopbackHost(u.Host) {
			return "", errors.New("public origin must use https unless it is loopback")
		}
	default:
		return "", errors.New("public origin must be http or https")
	}
	return u.Scheme + "://" + u.Host, nil
}

// requestHost prefers the Host header, which chi's RealIP/TrustedProxy chain
// leaves alone. X-Forwarded-Host is deliberately ignored: it is spoofable, and
// the only case where the request is trusted at all is loopback, where there is
// no proxy to have set it.
func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

// IsLoopbackHost reports whether a host[:port] refers to this machine. A name
// other than "localhost" is not resolved — DNS is attacker-influenceable, and a
// hostname that happens to point at 127.0.0.1 today is not a security property.
func IsLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// MCPResourcePath is the canonical path of the MCP endpoint. It is a constant
// rather than a route parameter because it is half of an audience identifier:
// the string a token is bound to must be the same string in every code path.
const MCPResourcePath = "/api/v1/mcp"

// MCPResource returns the canonical resource identifier tokens are bound to.
func (c Config) MCPResource(r *http.Request) (string, error) {
	origin, err := c.Origin(r)
	if err != nil {
		return "", err
	}
	return origin + MCPResourcePath, nil
}

// SameResource reports whether two resource identifiers name the same thing.
//
// Scheme and host are compared case-insensitively because RFC 3986 says case is
// not significant there; the path is compared exactly, because it is and
// because MCPResourcePath is a fixed lowercase constant anyway.
//
// A byte-for-byte comparison was the alternative and it rejected a client that
// spelled the host or scheme differently from the deployment's configuration —
// a legal way to name the same resource, answered with an `invalid_target` that
// gave the operator nothing to go on. Normalizing the *stored* value instead
// would have been worse: every grant issued before the change carries the old
// spelling as its audience, so lowercasing the canonical form would have
// retired all of them at once.
func SameResource(a, b string) bool {
	x, err := url.Parse(a)
	if err != nil {
		return false
	}
	y, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(x.Scheme, y.Scheme) &&
		strings.EqualFold(x.Host, y.Host) &&
		x.Path == y.Path
}

// CanonicalizeResource normalizes a client-supplied `resource` parameter enough
// to compare it, without being lenient about what it may contain.
//
// Per RFC 8707 the identifier must be absolute and carry no fragment. A
// trailing slash is the one difference tolerated, because clients disagree
// about it and rejecting it would break interoperability without buying
// anything: /api/v1/mcp and /api/v1/mcp/ cannot denote different resources
// here.
func CanonicalizeResource(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", errors.New("resource must be an absolute URL")
	}
	if u.Fragment != "" {
		return "", errors.New("resource must not include a fragment")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery = ""
	u.User = nil
	return u.String(), nil
}

// SPAURL builds an absolute URL to a route inside the dashboard SPA, honoring
// the base path the frontend was built with.
//
// The SPA origin and the API origin are the same in production and differ in
// local development, where Vite serves the app on its own port. That difference
// is why the base path is separate configuration rather than something derived
// from the request.
func (c Config) SPAURL(r *http.Request, route string) (string, error) {
	origin, err := c.spaOrigin(r)
	if err != nil {
		return "", err
	}
	return origin + normalizeBasePath(c.BasePath) + strings.TrimPrefix(route, "/"), nil
}

// normalizeBasePath turns APP_BASE_PATH into a URL path with exactly one
// leading and one trailing slash, and discards a value that is not a URL path
// at all.
//
// The discard is not defensive programming for its own sake; it is for one
// specific, recurring failure. MSYS2 shells — Git Bash, which is where `make
// dev-mcp` runs on Windows — rewrite a lone "/" in an environment variable into
// the MSYS installation root before handing it to a native binary, so
// APP_BASE_PATH arrives as "C:/Program Files/Git/". Concatenated here that
// produced consent links like
//
//	http://localhost:3000/C:/Program%20Files/Git/mcp-consent
//
// which 404s at the exact moment a human is trying to approve an agent, and
// gives them nothing to go on.
//
// A base path is a short URL fragment. A drive letter, a backslash, or a scheme
// in one is a mangled filesystem path rather than a deployment choice, so it is
// dropped in favour of the default and logged loudly. Serving the SPA from the
// root is wrong for a subpath deployment, but it is recoverable and visible;
// building an unusable URL is neither.
func normalizeBasePath(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "/"
	}
	if mangled := basePathDefect(base); mangled != "" {
		slog.Error("APP_BASE_PATH is not a URL path; ignoring it and serving the SPA from /",
			"value", base,
			"problem", mangled,
			"hint", "a shell may have rewritten it: set MSYS_NO_PATHCONV=1, or leave APP_BASE_PATH unset for /")
		return "/"
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base
}

// basePathDefect names what is wrong with a base path, or "" when it is usable.
func basePathDefect(base string) string {
	switch {
	case strings.Contains(base, `\`):
		return "contains a backslash"
	case strings.Contains(base, "://"):
		return "looks like an absolute URL rather than a path"
	case hasDriveLetter(base), strings.HasPrefix(base, "/") && hasDriveLetter(base[1:]):
		return "starts with a Windows drive letter"
	default:
		return ""
	}
}

func hasDriveLetter(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// spaOrigin resolves where the dashboard lives, which is the API's own origin
// everywhere except a split local-dev setup.
func (c Config) spaOrigin(r *http.Request) (string, error) {
	if c.SPAOrigin != "" {
		return NormalizeOrigin(c.SPAOrigin)
	}
	return c.Origin(r)
}
