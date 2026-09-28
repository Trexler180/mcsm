# Implementation Plan: Scoped Control-Plane Access Keys

## Selected Design And Constraints

Implement Option 2 from the capability-scoped agent-access proposal. External
automation authenticates to the existing API with `Authorization: Bearer
mcsm_pat_<secret>` over TLS. A key is owned by one user, allowlists one or more
servers, grants normalized server-permission scopes, expires within 90 days,
and can be rotated or soft-revoked. Authentication and authorization fail
closed. The API-to-worker protocol and worker token do not change.

Key authorization is an intersection:

`active key ∩ unexpired key ∩ allowed route ∩ allowed server ∩ key scope ∩ current user RBAC`

For admin owners, only the final current-user RBAC term has an admin bypass; the
key's route, server, and scope terms never do. Do not grant the `admin` server
scope to access keys in this phase, preventing membership administration,
server deletion, and clone/create paths.

## Source Revision And Drift Check

Planning revision: `f27626beb3e383f22e4f7b75137701097a916615`.
Evidence manifest digest:
`5cb823c5a8c8c02790b4e4801ab18120fe62202d4e6ec9b1ada801b449faa0a1`.

Before editing, confirm the relevant auth, router, server-access, audit, store,
and security UI files have not materially drifted. The untracked `deploy/`
directory is user-owned and out of scope.

## Affected Components

- `apps/api/migrations/026_agent_access_keys.sql`: additive key capability,
  lifecycle, usage, and audit-attribution columns/indexes.
- `apps/api/internal/store`: key model, token-hash lookup, create/list/rotate,
  soft revoke, stale usage update, permission validation support, and audit key
  attribution.
- `apps/api/internal/auth`: machine-principal context and opaque bearer parsing;
  keep store access behind a callback/interface to avoid the existing
  `store -> auth` package dependency becoming a cycle.
- `apps/api/internal/api`: machine route boundary, key-aware rate identity,
  permission intersection before admin bypass, and filtered server listing.
- `apps/api/internal/api/handlers`: human-only key lifecycle endpoints with
  password/TOTP reauthentication and generic errors.
- `apps/web`: account-security access-key card, one-time secret display, server
  and scope selection, expiry, rotate, and revoke interactions.
- `docs/security.md`, `docs/operations.md`, and API-facing examples: supported
  header format, lifecycle runbook, scope semantics, and incident response.

## Ordered Work Packages

### 1. Schema and model

Add `token_prefix`, `scopes`, `server_ids`, `last_used_at`, `last_used_ip`, and
`revoked_at` to `api_keys`, plus `api_key_id` to `audit_log`. Add useful indexes
for owner inventory and active-token lookup. Existing reserved rows receive
empty capability arrays and are therefore unusable. Preserve the existing
hashed-token uniqueness constraint. Add migration up/down coverage.

Acceptance: migrations apply from a fresh database and an upgraded schema;
rollback is structurally valid; existing rows cannot authenticate.

### 2. Store and secret lifecycle

Generate 32 random bytes with `crypto/rand`; encode in a URL-safe form beneath
the `mcsm_pat_` prefix. Store SHA-256 of the full presented token and only a
short non-secret display prefix. Normalize/deduplicate scopes using the existing
server-permission vocabulary, reject `admin`, require at least one server and
one scope, and require an expiry after now but no more than 90 days away.

Implement owner-scoped list/create/rotate/revoke methods. Rotation replaces the
hash atomically, clears stale usage fields, and makes the old token invalid as
soon as the update commits. Revocation is soft and idempotent. Authentication
rejects nil expiry, expiry at/before now, revocation, malformed metadata, and a
missing owner. Update usage metadata only if stale (for example five minutes)
or the IP changed, so polling does not cause a write per request.

Acceptance: store tests prove one-time raw-secret behavior, stored hash/prefix,
validation, expiry boundaries, immediate rotation/revocation, user-delete
behavior, and throttled usage writes.

### 3. Authentication principal and route boundary

Extend bearer authentication so JWT parsing remains the first path for JWT-like
tokens and only the reserved `mcsm_pat_` prefix reaches key lookup. Populate
ordinary user claims from the key owner plus a separate machine principal
containing key ID, normalized scopes, and allowed servers. Return uniform 401s
for every invalid-key state. Never accept a machine token through query
parameters and never mint a browser ticket from one.

Add middleware that permits machine principals only under the exact
`/api/v1/servers` route family. Human JWT behavior remains unchanged. Rate-limit
machine traffic per key identity (and retain the existing network-level
separation) so one noisy key does not consume another key's budget.

Acceptance: auth tests cover malformed prefix, unknown hash, expired/revoked
keys, user deletion, non-server routes, tickets, and rate-limit identity.

### 4. Authorization intersection and disclosure control

In `serverAccessGate`, resolve the URL server ID first for machine principals,
then require the server allowlist and relevant scope before evaluating the
owner's current permission/admin status. Group-read gates use the same group or
leaf semantics as user permissions. This key check must precede the global-admin
bypass. `requireAdmin` rejects machine principals.

Filter `GET /servers` for keys to the intersection of allowlisted servers and
servers the owner can currently access. Do not reveal inaccessible IDs or
differentiate forbidden from missing resources beyond existing behavior. The
special create/import routes remain inaccessible because machine principals
cannot satisfy `requireAdmin`.

Acceptance: matrix tests cover admin/non-admin owners, group/leaf scopes,
allowlisted/non-allowlisted servers, current permission removal, create/import,
member/admin routes, and server-list filtering.

### 5. Lifecycle HTTP API and step-up authentication

Add human-JWT-only routes beneath `/api/v1/auth/api-keys`:

- `GET /` lists metadata, never hashes or raw tokens.
- `POST /` creates a key and returns the raw token once.
- `POST /{id}/rotate` atomically rotates and returns the new raw token once.
- `DELETE /{id}` soft-revokes the caller's key and returns 204.

Create and rotate bodies require the current password and, when enabled, a
current TOTP code. Do not accept recovery codes for routine key issuance. Verify
the caller currently has every requested permission on every selected server;
admins satisfy user RBAC but remain key-scope bounded. Return generic
reauthentication failures and apply a dedicated attempt throttle or reuse the
existing safe authentication-throttle pattern. Audit create/rotate/revoke
without including secrets.

Acceptance: handler tests cover human-only access, password/TOTP cases,
cross-user IDs, no privilege escalation, invalid expiry/scopes/servers, secret
response shape, and idempotent revocation.

### 6. Audit attribution

Extend audit writes used by request handlers to accept an optional key ID and
store it in `audit_log.api_key_id` while retaining `user_id`. Background/system
actions remain null-key entries. Return the field in audit JSON and render a
compact access-key marker where audit entries are displayed. Never add token
prefixes or secrets to routine audit detail.

Acceptance: an audited key action identifies both owner and key; human and
background audit entries remain compatible; revocation does not erase history.

### 7. Security UI and documentation

Add an “Agent access keys” card to Account → Security. It explains that keys
act with real server authority, defaults to the narrowest diagnosis scopes,
requires explicit server selection and expiry, and requests password/TOTP at
creation/rotation. The raw secret appears in a copyable warning panel once and
is removed from component state when dismissed. List name, prefix, servers,
scopes, expiry, last use, IP, and revoked state; provide rotate/revoke confirms.

Document TLS-only use, environment/secret-manager injection, separate keys per
agent and purpose, a read-only diagnosis example, action-key examples,
rotation/revocation, incident response, and the deliberate prohibition on node
token sharing and query-string credentials.

Acceptance: TypeScript checks, lint, build, and focused component/API tests pass;
manual UI smoke verifies one-time display and responsive layout.

## Compatibility And Migration

The worker protocol, JWT format, browser ticket format, and all existing server
routes remain backward compatible. The new bearer prefix prevents ambiguity
with JWTs. Existing API-key rows are inert because empty scopes/server IDs and a
missing expiry fail closed. API responses add only optional audit metadata.

Do not edit generated files manually. Use migration `026` after confirming no
higher local migration exists at implementation time.

## Tactical Protections During Migration

- Do not expose or reuse node bearer tokens for testing.
- Keep machine authentication disabled until schema, authorization, and audit
  changes are present together.
- Fail closed on malformed JSON scope/server columns and store lookup errors.
- Keep key lifecycle routes JWT-only even before the UI lands.
- Preserve generic external error messages and detailed server-side logging
  without raw authorization headers.

## Tests And Security Validation

- Migration upgrade/down and fresh-schema tests.
- Store lifecycle and token non-disclosure tests.
- Authentication table tests for all credential states.
- Full server/scope/user permission intersection matrix, including an
  admin-owned narrowly scoped key.
- Route-boundary tests for every non-server route family and special `/servers`
  routes.
- Audit attribution and no-secret serialization/logging tests.
- Concurrency/race test for rotation and revocation.
- Web interaction tests for form validation, one-time display, and revoke.
- `go test ./...` in `apps/api`; repository `make test`; web `pnpm typecheck`,
  `pnpm lint`, and `pnpm build`.

## Performance And Resource Benchmarks

Benchmark authenticated `GET /servers/{id}/status` with a JWT and a key against
a realistic SQLite database. Record median and tail latency plus allocations.
Under sustained polling, observe database write count and WAL growth; usage
metadata must not write on every request. No fixed numeric threshold was
supplied, so flag any material regression and document the measured baseline
before choosing a cache. Do not introduce an in-memory authorization cache in
the first implementation because immediate revocation is a required invariant.

## Rollout And Rollback

Deploy with no keys, then create one read-only, short-lived key for a
non-critical server. Verify server-list filtering, diagnostics, denied actions,
usage metadata, and audit attribution through the public TLS endpoint. Rotate
it and prove the old token fails, then revoke it. Only then issue production
keys, separating diagnosis from mutation where practical.

To roll back, revoke every key, disable/remove the machine-auth branch, and
redeploy the prior API/web binaries. Existing JWT and worker communication stay
functional. Leave additive columns in place until a controlled database
maintenance window; do not drop them as part of emergency rollback.

## Acceptance Criteria

- No external agent needs or receives a node token, user password, refresh
  token, or browser JWT.
- A raw access key exists only in the create/rotate response and client secret
  store; the database contains only its hash and non-secret display prefix.
- An admin-owned key cannot access an unlisted server, an ungranted scope, or
  any non-server API route.
- Key revocation/rotation and user permission changes are effective on the next
  request.
- Machine actions are attributable to both user and key, without secret leakage.
- All focused, component, migration, and repository checks pass.
- Public documentation states TLS, least-privilege, expiry, rotation, and
  incident-response requirements and forbids direct node-token distribution.

## Open Decisions

- Confirm whether the production maximum key lifetime should be shorter than 90
  days; keep the implementation constant centralized either way.
- Decide whether read-only file access belongs in the default diagnosis preset;
  it can expose configuration secrets and should not be silently selected.
- Defer MCP-specific transport and approval workflows until an external client
  requirement justifies Option 3.

