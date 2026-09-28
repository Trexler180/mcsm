# Proposal: A Remote MCP Control Plane For Agents The Operator Already Runs

## The Situation

ServerManager can now be reached by a machine principal. The scoped access-key
work (`E02`–`E08`) gave automation a credential that is hashed at rest, bounded
by an explicit server allowlist and permission scopes, expiring, revocable,
rate-limited on its own identity, and audited against both the owning human and
the exact key. Nothing about that authorization model needs to change.

What is missing is the *hand-off*. A user who already runs Claude Code or Codex
and wants it to look at a struggling Minecraft server has exactly one supported
route: open the panel, re-authenticate, mint a 90-day secret, and then move that
secret — by hand — into whatever the agent reads. In practice the secret ends up
in a shell history, a dotfile, a repository, or the chat itself. It is a bearer
credential with real authority over real servers, and it stays valid until
someone remembers it exists.

The user asked for something specific: point an agent at ServerManager,
diagnose problems, request tightly controlled actions, **without installing a
local connector and without pasting a reusable API key into a chat.**

Those two constraints are doing a lot of work. Together they eliminate the local
stdio shim (an install) and the PAT (a pasted reusable key), and they leave one
thing standing: the panel is a web application, the user is already signed into
it with a password and, if they enabled it, TOTP. A browser consent screen is
the one place in this system where a human can make an authorization decision
with full context and no secret ever touching their hands.

## Options

### Option 1 — Remote MCP authenticated by a copied access key (baseline)

Serve the MCP tool facade at an HTTP endpoint and authenticate it with the
`mcsm_pat_` bearer that already works. The client is configured once by hand.

**What it gets right.** It is genuinely the smallest change. `auth.Middleware`
already routes the reserved prefix to `AuthenticateAccessKey`, `serverAccessGate`
already computes the intersection, `rateKey` already buckets per key, and
`LogActionWithKey` already attributes. Adding the tool facade on top of that is
almost all of the work and none of the risk. It is also the honest fallback: if
a client's OAuth implementation turns out to be broken in a way we cannot work
around, this path still delivers a working, bounded agent integration.

**Where it fails the request.** The user still handles a reusable secret, which
is the thing they asked to avoid. Beyond the UX, three properties are missing:

- *No consent record.* The key knows which servers and scopes it may use, but
  nothing records that a specific client was authorized, by whom, when, for how
  long, or with what understanding of what it could do.
- *No per-client identity.* Two agents sharing one key are indistinguishable in
  the audit log. Giving each its own key is possible and recommended, but it is
  a discipline, not a property of the design.
- *Rotation is manual and machine-by-machine.* Rotating a key used from a laptop
  and a workstation means editing configuration in two places, and the window
  between them is a broken agent.

**Verdict.** Implement the facade so this path *works*, keep it **disabled by
default** for MCP, and document why. It is a fallback, not the answer.

### Option 2 — Embedded remote Streamable HTTP MCP with OAuth browser consent (selected)

Add two things to the existing API process: an OAuth 2.1 authorization server,
and an MCP resource protected by it.

The flow a user actually sees:

1. They run `claude mcp add --transport http servermanager https://panel/api/v1/mcp`
   (or paste an equivalent five-line Codex entry). No token in the command.
2. The client fetches protected-resource metadata from the MCP endpoint, follows
   it to the authorization-server metadata, registers itself if it needs to, and
   opens a browser at `/authorize`.
3. `/authorize` requires a live dashboard session. If the user is not signed in,
   they sign in — with MFA if they have it — and land back on the consent screen.
   No session, no consent: it fails closed, and an access key or another machine
   credential can never satisfy it.
4. The consent screen names the client, the capabilities requested, the exact
   servers to be exposed, the grant's duration, and states plainly that
   start/stop/restart still require a separate approval from a human.
5. They click Authorize. A durable grant row is created. A single-use,
   short-lived, PKCE-bound authorization code goes back to the client's exact
   registered loopback redirect.
6. The client exchanges it for a short-lived access token bound to this exact
   MCP resource, plus a rotating refresh token. The human never sees either.

The authority behind any subsequent tool call is:

```
active OAuth grant ∩ unexpired, audience-bound access token
                  ∩ approved server allowlist ∩ approved MCP scopes
                  ∩ the owner's live ServerManager RBAC
```

Every term is re-read per call. Revoking the grant, deleting or demoting the
owner, removing them from a server, or narrowing their leaf permissions all
land on the very next tool call, because none of it is cached.

**The tool surface is a facade, not a proxy.** This is the single most important
design decision and it is worth stating as a prohibition rather than a
principle. The MCP endpoint exposes named tools with typed, validated arguments.
It does not accept a URL, an API path, a shell command, SQL, a filesystem path,
an environment variable name, a node id, or a raw Minecraft console command. A
model that decides it wants to `GET /api/v1/settings/integrations` has no way to
express that wish.

The initial tools are five bounded reads — `list_servers`,
`get_server_diagnostics`, `get_recent_log_events`, `get_metrics_history`,
`list_server_audit` — plus `request_server_action` and `get_action_request`.

**Actions go through a server-side approval queue.** `request_server_action`
does not act. It creates a short-lived pending request naming the server, the
action (`start`, `stop`, or `restart` only), and the agent's stated reason, and
returns an id. A human sees it in the dashboard, with the evidence, the client
that asked, and an expiry. Approving it requires the same password + TOTP
step-up the panel already uses for issuing credentials. Only then does the
server re-check the owner's live permission and the grant, claim the request
with an atomic conditional state transition, and execute exactly once.

The reason for not simply trusting the MCP host's "allow this tool call?" prompt
is that it puts the security boundary inside software ServerManager does not
control, does not version, and cannot audit. A host-side prompt is a fine
*additional* layer; it is not the layer that decides whether a Minecraft server
restarts.

**Prompt injection is treated as the default condition, not an edge case.** Log
lines, mod names, MOTDs, and player names are written by parties the operator
does not control. The MCP server's instructions are static, versioned
application text — never derived from any of that — and they tell the client
explicitly that returned server content is untrusted evidence which must never
override instructions or be used to request broader access. Tool outputs
separate trusted metadata (which server, which node, what the panel believes the
status is) from untrusted evidence (what the log said), redact anything that
looks like a secret, and are hard-capped so a hostile log cannot flood the
model's context.

**Costs, stated plainly.** An OAuth authorization server is security-critical
code running inside the API's trust boundary. PKCE, exact redirect matching,
resource/audience binding, code single-use, refresh rotation with replay-safe
family revocation, consent CSRF, open-redirect avoidance, and conservative
limits on unauthenticated endpoints are each a way to get this badly wrong.
That is why the implementation plan makes each one a named work package with
its own tests rather than a sentence in a design document.

### Option 3 — Local sidecar or device pairing

Ship a small local process that speaks stdio MCP to the agent and holds a
device-bound key established by a one-time pairing code shown in the panel.

**What it gets right.** It is the strongest option on paper. The key never
traverses a browser redirect, can be bound to the device or to OS-level
protected storage, and there is no authorization-code flow to get wrong. It also
sidesteps every question about whether a given client's OAuth implementation is
correct.

**Why it is not selected.** It requires installing and updating software on the
operator's machine, which the request explicitly forbids. That constraint is not
arbitrary: it is what makes the feature usable by someone who already has an
agent and does not want a new dependency. Beyond the constraint, a sidecar adds
a second holder of delegated authority on an endpoint the panel cannot attest,
a per-OS packaging and signing burden, and version-skew as a supported failure
mode.

**Verdict.** Record it. Revisit it only if a required client turns out to be
unable to complete a remote authorization-code flow.

## Comparison

| Dimension | 1. Copied access key | 2. Embedded OAuth MCP | 3. Local sidecar |
| --- | --- | --- | --- |
| Local install required | No | No | **Yes — disqualifying** |
| Human handles a reusable secret | **Yes** | No | No |
| Consent record (client, scope, duration) | None | Durable, revocable grant | Pairing record |
| Per-client identity in audit | Key name only | Client + grant + owner | Device + owner |
| Revocation granularity | Per key | Per grant, immediate | Per device |
| Mutations gated by a human on the panel | No | **Yes, with step-up** | Would need the same work |
| New security-critical code in the API | Minimal | **OAuth AS + tool facade** | Pairing + signing |
| New distributable artifact | No | No | **Yes** |
| Works with an unmodified Claude Code / Codex | Yes | Yes | After install |

## Recommendation

Implement Option 2.

It is the only option that satisfies both stated constraints, and it is the only
one that turns "an agent can reach my servers" into a thing the owner can see,
bound, and take back. The additional security-critical code is real and is the
price; the implementation plan spends its length on exactly that code.

Keep Option 1 implemented but disabled by default, so a verified client
incompatibility has a documented, bounded escape hatch rather than an emergency
redesign. Keep Option 3 on file for the case where remote OAuth is genuinely
unavailable.

## Invariants This Proposal Commits To

- `effective authority = active OAuth grant ∩ unexpired/audience-bound token ∩
  approved server allowlist ∩ approved MCP scopes ∩ the owner's live
  ServerManager RBAC`, re-evaluated on every tool call with no cache.
- Consent requires a live human dashboard session and fails closed. A machine
  credential can never authorize another credential.
- Tokens are high-entropy, hash-only at rest, never in a URL, never in a log,
  never in documentation, and bound to the exact MCP resource. There is no
  token passthrough to any downstream API.
- Authorization codes are single-use, short-lived, PKCE-`S256`-bound, and tied
  to an exactly-matched redirect URI and a canonicalized resource.
- Refresh rotation is replay-safe: presenting a rotated refresh token revokes
  its whole family.
- An MCP client never holds global administrative authority, whoever owns it.
- No mutating action executes without a fresh human approval, a re-check of live
  authority at execution time, and an at-most-once atomic transition.
- MCP server instructions are static versioned application text. All returned
  server content is labelled untrusted evidence, redacted, and size-bounded.
