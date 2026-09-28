package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	apimw "github.com/mcsm/api/internal/api/middleware"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/mcpserver"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// The MCP resource is mounted outside the JWT-authenticated group, so every
// protection it needs is applied explicitly here rather than inherited:
//
//	body cap → timeout → rate limit → bearer verification → tool facade
//
// Nothing below trusts a value from the request except the bearer token, and
// the token is only ever resolved against the store.

const (
	// mcpMaxBodyBytes bounds one JSON-RPC request. Tool arguments here are ids,
	// small counts, and a short reason string; a megabyte is already generous.
	mcpMaxBodyBytes = 1 << 20

	// mcpRequestTimeout bounds a tool call. Diagnostics read the database and
	// may reach a node for live stats, so it is longer than a plain API read
	// and far shorter than the lifecycle calls, which the facade never makes
	// on this path — an approved action is executed from the dashboard.
	mcpRequestTimeout = 3 * time.Minute
)

// mcpPrincipalKey carries the resolved delegation from the bearer middleware to
// the handler. It is a private type so nothing outside this file can plant one.
type mcpPrincipalKey struct{}

// mountMCP wires the MCP resource and its OAuth discovery documents onto the
// router, and reports whether the surface is reachable at all.
//
// The whole feature is inert unless a public origin resolves: tokens are bound
// to an absolute resource identifier, and a deployment that cannot state its
// own external address cannot mint one honestly. Returning early leaves the
// routes unmounted, so an unconfigured deployment 404s rather than half-working.
func mountMCP(r chi.Router, s *store.Store, urls publicurl.Config, operator mcpserver.OperatorBackend, migrations mcpserver.MigrationStarter, extra ...mcpserver.Option) {
	// Nothing to check here beyond construction: whether a given *request* can
	// resolve an origin is decided per request, because the loopback fallback
	// depends on where the request arrived from.
	opts := append([]mcpserver.Option{
		mcpserver.WithOperatorBackend(operator),
		mcpserver.WithMigrationStarter(migrations),
	}, extra...)
	svc := mcpserver.New(s, opts...)

	// A dedicated limiter, separate from the authenticated one, because these
	// buckets belong to a different population. A generous sustained rate: an
	// agent polling an action request every few seconds is normal use.
	limiter := apimw.NewRateLimiter(600, 120)

	handler := mcp.NewStreamableHTTPHandler(
		func(req *http.Request) *mcp.Server {
			p, _ := req.Context().Value(mcpPrincipalKey{}).(*mcpserver.Principal)
			if p == nil {
				// Unreachable: the bearer middleware refuses the request before
				// this runs. A server with no principal would still authorize
				// nothing, because every tool re-checks, but returning nil here
				// makes the invariant explicit rather than incidental.
				return nil
			}
			return svc.Server(p)
		},
		&mcp.StreamableHTTPOptions{
			// Stateless: every tool call is a self-contained authorization
			// decision. Backup confirmation uses the protocol's multi-round-trip
			// input-required result, so no authority or approval lives in a server
			// session; clients without that confirmation path fail closed.
			Stateless:    true,
			JSONResponse: true,
		},
	)

	protected := apimw.MaxBodyBytes(mcpMaxBodyBytes)(
		mcpTimeout(
			limiter.KeyedMiddleware(mcpRateKey)(
				requireMCPToken(s, urls)(handler),
			),
		),
	)

	// Exactly this path, and nothing below it. Streamable HTTP is a single
	// endpoint — method and session header carry the whole protocol, so there
	// is no sub-path to serve — while the dashboard's own delegation screens
	// live under the same prefix (/api/v1/mcp/connection, /mcp/grants,
	// /mcp/action-requests) inside the session-authenticated group. A wildcard
	// here is more specific than that group's /api/v1/* and would capture them,
	// answering a signed-in operator with the bearer challenge meant for agents.
	r.Handle(publicurl.MCPResourcePath, protected)
}

// mcpTimeout bounds a single tool call.
func mcpTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), mcpRequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mcpRateKey buckets by delegation where one is known, and by source address
// before the token has been verified.
//
// This runs *before* verification, so in practice it is the IP bucket that
// applies to unauthenticated probing — which is the traffic worth limiting
// there. Keeping the grant branch means the key is still right if the limiter
// is ever reordered behind verification.
func mcpRateKey(r *http.Request) string {
	if p, ok := r.Context().Value(mcpPrincipalKey{}).(*mcpserver.Principal); ok && p != nil {
		return "mcp:" + p.GrantID
	}
	return apimw.ClientIPKey(r)
}

// requireMCPToken verifies the bearer token and attaches the delegation it
// resolves to.
//
// The SDK's middleware owns the 401 and its `WWW-Authenticate` header, which is
// what lets an unauthenticated client discover where to authorize. The verifier
// below owns the decision, and it delegates that entirely to the store: a
// token is valid only while it is unexpired, unrevoked, bound to this exact
// resource, under an active grant, owned by a live user. None of that is
// cached, so revocation lands on the very next call.
func requireMCPToken(s *store.Store, urls publicurl.Config) func(http.Handler) http.Handler {
	explainAccessKeys := envEnabled("MCP_EXPLAIN_ACCESS_KEY_REJECTION")

	verifier := func(ctx context.Context, token string, req *http.Request) (*mcpauth.TokenInfo, error) {
		resource, err := urls.MCPResource(req)
		if err != nil {
			// No canonical resource means no audience to check against, and a
			// guessed one would authorize the wrong thing.
			slog.Error("mcp: public origin unavailable for token verification", "error", err)
			return nil, mcpauth.ErrInvalidToken
		}

		principal, err := s.AuthenticateMCPAccessToken(ctx, token, resource)
		if err != nil {
			if explainAccessKeys && strings.HasPrefix(token, auth.AccessKeyPrefix) {
				return nil, errors.Join(mcpauth.ErrInvalidToken, errMCPAccessKeyUnsupported)
			}
			return nil, mcpauth.ErrInvalidToken
		}

		// Usage metadata, recorded from the row we just read (which still holds
		// the previous timestamp, making the staleness check free). A failed
		// write must never turn a valid credential into a 401.
		if err := s.TouchMCPGrant(ctx, principal.Grant, clientIP(req)); err != nil {
			slog.Warn("mcp grant usage update failed", "grant_id", principal.Grant.ID, "error", err)
		}

		return &mcpauth.TokenInfo{
			Scopes:     principal.Scopes,
			Expiration: principal.TokenExpiresAt,
			UserID:     principal.User.ID,
			Extra: map[string]any{
				"principal": &mcpserver.Principal{
					UserID:     principal.User.ID,
					GrantID:    principal.Grant.ID,
					ClientName: principal.Grant.ClientName,
					IP:         clientIP(req),
					Scopes:     principal.Scopes,
					ServerIDs:  principal.Grant.ServerIDs,
				},
			},
		}, nil
	}

	return func(next http.Handler) http.Handler {
		attach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := mcpauth.TokenInfoFromContext(r.Context())
			if info == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			p, _ := info.Extra["principal"].(*mcpserver.Principal)
			if p == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpPrincipalKey{}, p)))
		})

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The metadata URL is per-request because the origin can be resolved
			// from a loopback request in local development.
			metadataURL := ""
			if origin, err := urls.Origin(r); err == nil {
				metadataURL = origin + "/.well-known/oauth-protected-resource" + publicurl.MCPResourcePath
			}
			mcpauth.RequireBearerToken(verifier, &mcpauth.RequireBearerTokenOptions{
				ResourceMetadataURL: metadataURL,
			})(attach).ServeHTTP(w, r)
		})
	}
}

// errMCPAccessKeyUnsupported explains the one confusing rejection: a valid
// agent access key presented to the MCP endpoint.
//
// Access keys are never an MCP credential. They are long-lived and copied into
// config files, which is exactly the property this feature exists to avoid, so
// there is no setting that admits one — MCP_EXPLAIN_ACCESS_KEY_REJECTION only
// decides whether the 401 says *why*, and the key is refused either way.
var errMCPAccessKeyUnsupported = errors.New(
	"agent access keys are not accepted here; connect this client with the OAuth flow")

// envEnabled reports whether an environment variable is set to "true".
func envEnabled(name string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(name)), "true")
}

// clientIP uses RemoteAddr after the router's trusted-proxy middleware has
// canonicalized it. Reading X-Forwarded-For here would let a direct MCP client
// forge grant-usage and audit attribution.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
