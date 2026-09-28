# Implementation Plan: Embedded OAuth-Protected Remote MCP

## Selected Design And Constraints

Implement Option 2 from the [remote MCP proposal](../proposals/remote-mcp-control-plane.md):
an OAuth 2.1 authorization server plus a Streamable HTTP MCP resource, both
inside the existing Go API process, using
`github.com/modelcontextprotocol/go-sdk` v1.7.0 (which negotiates protocol
versions `2024-11-05` … `2026-07-28`, so a client pinned to an older revision
still connects).

The invariant every part of this plan serves:

```
effective authority = active OAuth grant
                    ∩ unexpired, audience-bound access token
                    ∩ approved server allowlist
                    ∩ approved MCP scopes
                    ∩ the owner's live ServerManager RBAC
```

Re-evaluated on **every** tool call, with no cache anywhere, because immediate
revocation is a required invariant and a cache is precisely a window in which a
revoked grant still works.

Two hard prohibitions frame the rest:

- **The MCP endpoint is a tool facade, never a proxy.** No tool accepts a URL,
  an API path, a shell command, SQL, a filesystem path, an environment or
  process reference, a node id, an integration secret name, or a raw Minecraft
  console command. There is no "call this endpoint" escape hatch.
- **Nothing model-facing ever sees** a node bearer token, `.connection.cfg`,
  `secrets.env`, an integration secret, a password hash, a session, a JWT, an
  access key, or another user's data.

## Source Revision And Drift Check

Planning revision `f27626beb3e383f22e4f7b75137701097a916615`, working tree
dirty with the scoped access-key foundation. Evidence manifest digest
`8a71009c033820b467ae6aaa14f9c92bd065b30192a807d04b8f63c059271a70`. Full
drift list in [context.md](../context.md).

Before editing, re-read `auth/middleware.go`, `auth/machine.go`,
`api/server_access.go`, `api/router.go`, `store/apikeys.go`, `store/audit.go`,
and `api/handlers/apikeys.go` and confirm they still match the digests
recorded there. The untracked `deploy/` directory is user-owned and out of
scope.

## Affected Components

- `apps/api/go.mod` — add the MCP SDK (pulls a `golang-jwt/jwt/v5` patch bump).
- `apps/api/migrations/027_mcp_oauth.sql` — clients, authorization requests,
  codes, grants, tokens, action requests, and one more audit actor column.
- `apps/api/internal/store/mcp.go` — the OAuth and grant model, stored
  **separately** from `api_keys` so the two credential families cannot be
  confused, while reusing the same permission vocabulary and audit concepts.
- `apps/api/internal/store/mcp_actions.go` — the action approval queue with its
  at-most-once transition.
- `apps/api/internal/mcpserver/` — the tool facade: static instructions, the
  per-request authorization context, the five diagnostic tools, the two action
  tools, output bounds, and redaction.
- `apps/api/internal/api/handlers/mcpoauth.go` — authorization server HTTP
  surface (metadata, `/authorize`, consent read/decide, `/token`, `/register`,
  `/revoke`).
- `apps/api/internal/api/handlers/mcpgrants.go` — owner-facing grant and
  approval endpoints.
- `apps/api/internal/api/mcp_mount.go`, `apps/api/internal/api/publicorigin.go`,
  `apps/api/internal/api/router.go` — mounting, limits, and canonical URLs.
- `apps/api/internal/api/middleware/ratelimit.go` — a second limiter usable
  outside the authenticated group.
- `apps/web` — the consent route, the "Remote agent connections" card, pending
  approvals, and the connect instructions.
- `docs/` and `scripts/` — threat model, setup, demo, revocation, limits.

## Canonical URLs And Public Origin

| Thing | Value |
| --- | --- |
| Issuer | `<public origin>` |
| MCP resource | `<public origin>/api/v1/mcp` |
| Protected-resource metadata | `/.well-known/oauth-protected-resource/api/v1/mcp` (and the bare `/.well-known/oauth-protected-resource`) |
| AS metadata | `/.well-known/oauth-authorization-server` (and the path-inserted `/…/api/v1/oauth`, plus OIDC aliases) |
| Authorization | `<public origin>/api/v1/oauth/authorize` |
| Token | `<public origin>/api/v1/oauth/token` |
| Registration | `<public origin>/api/v1/oauth/register` |
| Revocation | `<public origin>/api/v1/oauth/revoke` |

Public origin resolution, in order:

1. `MCP_PUBLIC_ORIGIN` — an absolute origin with no path, query, or fragment.
2. `APP_ORIGIN` — the value production already sets.
3. The request's own `Host`, **only** when that host is loopback. This keeps
   `make dev-api` and the demo working without configuration and cannot be
   steered by an attacker's `Host` header in a real deployment.

Anything else fails closed with a generic 503 and a server-side log naming the
missing variable. `http` is accepted only for loopback origins; every other
origin must be `https`.

The consent page lives in the SPA, whose base path differs between deployments,
so its URL is `APP_ORIGIN + APP_BASE_PATH + "mcp-consent"` with `APP_BASE_PATH`
defaulting to `/`. Production sets `/dashboard/`. This is the only place the
two origins can differ (in local dev the SPA is on the Vite port and the MCP
resource is on the API port), which is exactly the local-dev case.

Because discovery is origin-rooted, a reverse-proxied deployment **must** also
forward `/.well-known/oauth-protected-resource*` and
`/.well-known/oauth-authorization-server*` to the API. `docs/deployment.md`
gets the `location` block.

## Ordered Work Packages

### 1. Schema

`027_mcp_oauth.sql`, additive only:

- `mcp_clients` — `client_id`, `client_name`, `redirect_uris` (JSON), `origin`
  (`dynamic` | `preregistered`), `created_at`, `last_registered_at`,
  `software_id`, `logo_uri`. Public clients only; there is no secret column,
  because issuing a pretend secret to a client that cannot keep one is worse
  than issuing none.
- `mcp_authorization_requests` — the in-flight `/authorize` state a consent
  screen resolves: `id`, `client_id`, `redirect_uri`, `state`,
  `code_challenge`, `code_challenge_method`, `scope`, `resource`,
  `expires_at`, `resolved_at`.
- `mcp_authorization_codes` — `code_hash` (unique), `request_id`, `grant_id`,
  `expires_at`, `consumed_at`.
- `mcp_grants` — `id`, `user_id`, `client_id`, `client_name` (snapshot),
  `scopes` (JSON), `server_ids` (JSON), `created_at`, `expires_at`,
  `revoked_at`, `last_used_at`, `last_used_ip`.
- `mcp_tokens` — `id`, `grant_id`, `kind` (`access` | `refresh`),
  `token_hash` (unique), `family_id`, `resource`, `scopes` (JSON),
  `issued_at`, `expires_at`, `revoked_at`, `used_at`.
- `mcp_action_requests` — `id`, `grant_id`, `user_id`, `server_id`, `action`,
  `reason`, `status`, `created_at`, `expires_at`, `decided_at`, `decided_by`,
  `executed_at`, `failure_reason`.
- `audit_log.mcp_grant_id` — `REFERENCES mcp_grants(id) ON DELETE SET NULL`,
  mirroring `api_key_id`: revoking or deleting a grant must never erase the
  history of what it did.

All child tables cascade from their owner/grant so deleting a user removes
their delegation state. Indexes: token-hash lookup, grant-by-owner, pending
requests by owner, and expiry sweeps.

Acceptance: fresh and upgraded migration tests pass; down migration is
structurally valid; no existing row's behaviour changes.

### 2. OAuth store

Secret handling copies the access-key rules exactly, because they are already
right: 32 bytes from `crypto/rand`, URL-safe unpadded encoding, a reserved
prefix, SHA-256 at rest, and one uniform "invalid" error for every failure
state. Prefixes: `mcsm_mcpc_` (code), `mcsm_mcpa_` (access), `mcsm_mcpr_`
(refresh) — all distinct from `mcsm_pat_`, so the two credential families can
never be confused at the boundary or in a log.

Lifetimes: code 60 s, access token 15 min, refresh token 30 d, grant 30 d by
default and 90 d maximum. A refresh token never outlives its grant.

Rotation is replay-safe: refreshing marks the presented token used inside the
same statement that would fail if it were already used, and does so in the
same transaction that issues the replacement pair, so a failed rotation
consumes nothing. Presenting an already-used refresh token more than
`MCPRefreshReuseGrace` (30 s) after its rotation revokes the grant and every
token under it; inside the grace it is a retry or a concurrent session and gets
a sibling pair in the same family. Consuming an authorization code twice
revokes the grant.

`AuthenticateMCPAccessToken` returns a principal only when the token is
unexpired, unrevoked, bound to this exact resource, its grant is active and
unexpired, and its owner still exists. Everything else is
`ErrMCPTokenInvalid`.

Acceptance: store tests for one-time code use, code replay revoking the grant,
refresh rotation, refresh replay revoking the family, expiry boundaries,
resource mismatch, grant revocation, and owner deletion.

### 3. Authorization server HTTP surface

- `GET` metadata endpoints — static JSON built from the resolved public origin,
  cacheable, CORS-open (they are public by definition), listing `code`,
  `authorization_code` + `refresh_token`, `S256` only, `none` token-endpoint
  auth, and the MCP scope vocabulary.
- `GET /api/v1/oauth/authorize` — strict validation *before* anything is
  persisted: known `client_id`, `redirect_uri` matched **exactly** against the
  client's registered set, `response_type=code`, `code_challenge_method=S256`,
  a `resource` that canonicalizes to exactly the MCP endpoint, and a scope set
  ⊆ the supported vocabulary. An unknown client or an unmatched redirect URI
  renders an error page and **never** redirects — that is the open-redirect
  boundary. Other errors redirect to the (already validated) redirect URI with
  an OAuth `error` code. On success it stores the request and 302s the browser
  to the SPA consent page.
- `GET /api/v1/oauth/consent?request=…` — JWT-authenticated, human-only.
  Returns what the screen must show: client name and registration origin,
  requested capabilities in plain language, the servers the caller may actually
  delegate (intersected with live RBAC), the redirect host, and the duration.
- `POST /api/v1/oauth/consent` — JWT-authenticated, human-only. Approve or
  deny. On approve it re-checks that the caller currently holds each mapped
  permission on each selected server, creates the grant, mints a single-use
  code, marks the request resolved, and returns a `redirect_to` built entirely
  from the **stored** redirect URI. CSRF is structurally impossible: the
  endpoint requires an `Authorization` header a cross-site form cannot set, and
  the request id is unguessable and one-shot.
- `POST /api/v1/oauth/token` — form-encoded, `authorization_code` and
  `refresh_token`. Verifies PKCE `S256`, exact redirect URI, client identity,
  and resource. Never accepts a client secret. Emits `access_token`,
  `token_type`, `expires_in`, `refresh_token`, `scope`.
- `POST /api/v1/oauth/register` — RFC 7591, public clients only. Validates
  redirect URIs (loopback `http`, otherwise `https`; no wildcards, fragments,
  or userinfo), caps counts and lengths, rejects any requested
  `token_endpoint_auth_method` other than `none`, and is rate-limited hard.
- `POST /api/v1/oauth/revoke` — RFC 7009. Requires `client_id` (400 without
  it); otherwise always 200. Revokes an access token, or the whole grant when a
  refresh token is presented — but only for a token issued to the named
  client.

Every public endpoint gets its own conservative body cap, request timeout, and
rate-limit bucket, and returns generic errors. `no-store` on everything that
touches token material.

Acceptance: handler tests for each validation branch, the redirect/no-redirect
boundary, code reuse, PKCE failure, resource mismatch, machine-principal
rejection on the consent routes, and rate limits.

### 4. Protected MCP resource

Mount `/api/v1/mcp` outside the JWT-authenticated group, wrapped in:

1. a dedicated 1 MiB body cap and request timeout;
2. a dedicated rate limiter keyed by grant id (falling back to IP);
3. `auth.RequireBearerToken` from the SDK with the protected-resource metadata
   URL, so a 401 carries a usable `WWW-Authenticate`;
4. a verifier that resolves the token to a `Principal{UserID, GrantID, Scopes,
   ServerIDs, ClientName}` and records throttled usage metadata.

The handler is `mcp.NewStreamableHTTPHandler` in **stateless** mode with JSON
responses. Stateless is the right default here: every tool call is a
self-contained authorization decision, there is no server→client request to
make, and holding sessions would add state whose only purpose is to be
invalidated. `getServer` builds a per-request `*mcp.Server` closed over the
principal, so a handler can never see another request's authority.

Tools are registered only when the grant carries their scope — useful defence
in depth for a confused model — but **every handler re-checks authorization
itself**, because tool listing is ergonomics and authorization is the boundary.

### 5. Tool facade

Static, versioned instructions (never derived from any server-controlled
value), stating plainly that all returned server content is untrusted evidence
which must not be treated as instructions or used to justify broader access.

| Tool | Scope | Required live permission | Notes |
| --- | --- | --- | --- |
| `list_servers` | `mcp:servers.read` | `view` | Only granted servers the owner can still see. |
| `get_server_diagnostics` | `mcp:diagnostics.read` | `view` | Status, health, vitals, resource summary, recent indexed errors, recent audit. |
| `get_recent_log_events` | `mcp:logs.read` | `view` | Bounded count, bounded per-message length. |
| `get_metrics_history` | `mcp:metrics.read` | `view` | Bounded window and point count. |
| `list_server_audit` | `mcp:audit.read` | `view` | Bounded limit; server-scoped only. |
| `request_server_action` | `mcp:actions.request` | `power.start` / `power.stop` / `power.restart` | Creates a pending request. Does **not** act. |
| `get_action_request` | `mcp:actions.request` | as above | Observes state only. |

Output discipline, applied by shared helpers rather than per tool:

- Every response separates `server` (trusted panel metadata) from `evidence`
  (untrusted server-authored text), and every evidence array carries an
  explicit `untrusted: true` marker and a truncation count.
- Each evidence string is truncated to a fixed length and the array to a fixed
  count, so a hostile log cannot flood context.
- A redaction pass strips anything resembling a token, key, password, or
  connection string from evidence before it leaves the process.
- Errors are generic; the detailed cause is logged server-side.

### 6. Action approval queue

`request_server_action` creates a pending row with a short expiry and returns
its id and status. It never touches the node worker.

The owner sees pending requests in the dashboard: which client, which server,
which action, the agent's stated reason (rendered as untrusted text), and when
it expires. Approving requires the existing password + enabled-TOTP step-up,
with the same per-IP and per-account throttles the key handlers use.

Execution is at most once by construction:

```sql
UPDATE mcp_action_requests
   SET status = 'executing', decided_at = ?, decided_by = ?
 WHERE id = ? AND status = 'pending' AND expires_at > ?
```

Only the caller that gets `RowsAffected() == 1` proceeds. It then re-checks the
grant (still active, still lists this server, still carries the scope) **and**
the owner's live permission for the specific action, and only then calls the
node worker, finishing at `executed` or `failed`. A repeated approval, a
concurrent approval, and agent polling all observe state; none of them repeats
the action.

Audit entries name the human owner, the grant, and the client for request,
approval, denial, execution, and failure.

### 7. UI and documentation

Account → Security gains a **Remote agent connections** card:

- A Connect panel with the MCP URL, the exact `claude mcp add --transport http`
  command, and a Codex config snippet. **No token appears in any snippet** —
  that is the point of the feature.
- Active grants: client, servers, capabilities, created, expires, last used,
  and Revoke.
- Pending action approvals with the step-up fields, plus Approve/Deny.

A new `/mcp-consent` route renders the consent screen. It is a normal
authenticated SPA route, so an unauthenticated visitor is sent to login and
comes back — which is exactly the "live human session, fail closed" property,
obtained from machinery that already exists.

Docs: threat model, Claude Code and Codex setup, the local demo, revocation,
action approval, trusted-proxy and TLS requirements, and limitations.

### 8. Local demo and smoke test

A script that starts the API with loopback-safe MCP settings and prints the
exact client command, without writing to any global agent configuration and
without baking credentials into a tracked file. A Go smoke test drives
initialize → tools/list → tools/call against an in-process server with a
minted grant, proving negotiation and schemas without a real client.

## Compatibility And Migration

JWT sessions, tickets, access keys, the worker protocol, and every existing
route are untouched. All new tables and the one new audit column are additive.
The new bearer prefixes cannot collide with `mcsm_pat_` or a JWT. MCP is off
unless a public origin resolves, and PAT authentication for MCP does not exist:
an access key presented to the MCP endpoint is always refused, so it cannot
quietly become the supported path. (This plan originally proposed
`MCP_ALLOW_ACCESS_KEYS` as an opt-in. What shipped never admitted the key — the
flag only chose whether the 401 explained itself — and it is now spelled
`MCP_EXPLAIN_ACCESS_KEY_REJECTION` to say so.)

## Tactical Protections During Migration

- Never log an `Authorization` header, a raw token, a code, or a verifier.
- Fail closed on malformed JSON in any scope/server column.
- Keep consent and grant-management routes human-only even before the UI lands.
- Do not add an authorization cache. If profiling later demands one, it needs
  its own design for immediate revocation first.

## Tests And Security Validation

Protocol and schema: initialize negotiation across supported versions, tool
list shape, input-schema validation, structured output.

OAuth: metadata contents; PKCE required and `S256`-only; wrong verifier
rejected; exact redirect matching; unknown client renders rather than
redirects; resource required, canonicalized, and audience-bound; code
single-use with replay revoking the grant; access-token expiry; refresh
rotation; refresh replay revoking the family; revocation endpoint; consent
requires a human session; machine principals rejected; registration validation
and limits; rate limits on every public endpoint.

Authorization: the full intersection matrix — grant revoked, grant expired,
owner demoted, owner removed from the server, server not in the allowlist,
scope not granted, admin owner still bounded — plus "no forbidden operation is
reachable" (no console, kill, reinstall, restore, file write/delete, settings,
task creation, node, user, or integration surface exists as a tool).

Output: bounds hold under adversarial input; redaction removes secret-shaped
strings; untrusted markers present.

Actions: approval executes at most once under concurrency; re-authorization at
execution time; expiry; denial; audit attribution naming owner, grant, and
client.

Commands: `gofmt` on touched Go files; `go test ./... -count=1` and `go vet ./...`
in `apps/api`; `go test ./... -count=1` in `apps/agent`; `pnpm test -- --run`,
`pnpm typecheck`, `pnpm lint`, `pnpm build` in `apps/web`; `git diff --check`.

## Rollout And Rollback

Locally: run the demo, connect a client, list servers, pull diagnostics,
request a restart, approve it, confirm exactly one restart, revoke the grant,
confirm the next call fails. Only then consider a real deployment, which
additionally needs the `.well-known` proxy block and TLS.

To roll back, revoke every grant and unset `MCP_PUBLIC_ORIGIN`/`APP_ORIGIN`'s
MCP enablement; the additive tables stay until a maintenance window.

## Acceptance Criteria

- A user connects an unmodified Claude Code or Codex with one command and a
  browser click, and no token appears in any command, config snippet, log, or
  document.
- Every tool call re-evaluates the full intersection; revocation, demotion,
  membership change, and allowlist change land on the next call.
- No tool can reach an arbitrary URL, path, command, file, secret, or node.
- No action executes without a fresh human approval, a live re-check, and an
  at-most-once transition.
- Tool output is bounded, redacted, and explicitly labelled untrusted.
- All focused, component, migration, and repository checks pass.

## Open Decisions

- Whether client-ID metadata documents should be supported. They require the
  server to fetch an operator-supplied URL, which is an SSRF surface; dynamic
  registration and preregistration cover the target clients, so this phase
  documents the omission rather than taking the risk.
- Whether production should cap the grant lifetime below 90 days.
- Whether a narrow pre-approval window for `restart` is worth the reduced
  approval friction, or whether that is exactly the erosion this design exists
  to prevent.
