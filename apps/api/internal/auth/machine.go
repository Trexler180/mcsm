package auth

import "context"

// AccessKeyPrefix marks a bearer credential as a machine access key rather than
// a JWT. It is reserved: a token carrying it is never parsed as a JWT, and a
// JWT (three base64url segments) can never carry it, so the two credential
// families stay unambiguous at the boundary.
const AccessKeyPrefix = "mcsm_pat_"

// MachineIdentity is what a valid access key resolves to: the human owner it
// acts as, plus the key's own capability bounds. The auth package deliberately
// owns this shape rather than importing the store — store already imports auth
// (for password hashing), so the dependency has to point this way.
//
// Scopes and ServerIDs are the key's bounds, not the owner's authority. They
// are intersected with the owner's live per-server permissions on every
// request; nothing here grants access on its own.
type MachineIdentity struct {
	KeyID     string
	UserID    string
	Email     string
	Role      string
	Scopes    []string
	ServerIDs []string
	// TokenHash identifies the exact secret presented (a rotation replaces
	// it). Long-lived connections re-check it; it is a digest, never the token.
	TokenHash string
}

// KeyLookup authenticates a presented access key. It must fail closed: an
// expired, revoked, unknown, malformed, or orphaned key returns an error, and
// so does any database failure. The router adapts the store to this signature.
//
// ip is the caller's address, passed so the implementation can record bounded
// usage metadata; it is never part of the authentication decision.
type KeyLookup func(ctx context.Context, presented, ip string) (*MachineIdentity, error)

// MachinePrincipal is the request-scoped machine context. Its presence is what
// distinguishes an automation request from a human one: route boundaries, the
// server access gate, rate-limit identity, and audit attribution all key off it.
type MachinePrincipal struct {
	KeyID     string
	Scopes    []string
	ServerIDs []string
	// TokenHash is the digest of the secret this request authenticated with.
	// A WebSocket outlives the request that opened it, so it re-checks this
	// against the store to notice a revocation or rotation.
	TokenHash string
}

// AllowsServer reports whether the key's allowlist covers a server id. An empty
// allowlist allows nothing — a key with no servers is inert by construction.
func (m *MachinePrincipal) AllowsServer(serverID string) bool {
	if m == nil || serverID == "" {
		return false
	}
	for _, id := range m.ServerIDs {
		if id == serverID {
			return true
		}
	}
	return false
}

// DelegatedActor describes an OAuth-backed MCP agent while it reuses a
// bounded application operation. It is deliberately separate from
// MachinePrincipal: an access key and an OAuth grant are different credential
// families and must never become interchangeable.
type DelegatedActor struct {
	UserID     string
	GrantID    string
	ClientName string
	IP         string
}

type delegatedActorKeyType struct{}

var delegatedActorKey delegatedActorKeyType

// WithDelegatedActor attaches audit attribution to an internal, already
// authorized operation. It does not authenticate or authorize by itself.
func WithDelegatedActor(ctx context.Context, actor *DelegatedActor) context.Context {
	return context.WithValue(ctx, delegatedActorKey, actor)
}

// DelegatedActorFrom returns MCP audit attribution, if present.
func DelegatedActorFrom(ctx context.Context) *DelegatedActor {
	actor, _ := ctx.Value(delegatedActorKey).(*DelegatedActor)
	return actor
}

// MachineFrom returns the machine principal for a request, or nil for an
// ordinary human (JWT or ticket) request.
func MachineFrom(ctx context.Context) *MachinePrincipal {
	m, _ := ctx.Value(machineKey).(*MachinePrincipal)
	return m
}

// IsMachine reports whether a request is acting under *any* machine credential:
// an access key, or an OAuth-backed MCP grant.
//
// Route guards call this rather than checking one family directly, because
// checking one and forgetting the other is precisely the mistake it exists to
// prevent — and the two do not look alike from inside a handler. A delegated
// MCP actor resolves through currentUserID to the human who owns the grant, so
// a guard that only refuses access keys would read a delegated request as that
// human and let a credential approve its own successor.
func IsMachine(ctx context.Context) bool {
	return MachineFrom(ctx) != nil || DelegatedActorFrom(ctx) != nil
}
