# Security Hardening Proposal: Capability-Scoped Agent Access

## Decision

We need a supported way for operator-controlled automation and AI agents to
diagnose and operate servers without exposing the host worker's node-wide bearer
secret. The decision is where machine identity and policy should live: directly
at the worker, inside the existing control plane, or in a new isolated gateway.

## Executive Recommendation

The complete option set is:

- **Option 1: distribute the node token.** This is the smallest implementation,
  but it intentionally bypasses user/server authorization and is unacceptable
  as the supported path.
- **Option 2: add scoped control-plane access keys.** Keys are revocable,
  expiring capabilities whose server and action bounds are intersected with the
  owning user's live RBAC. This is the recommended design.
- **Option 3: introduce an automation or MCP gateway.** A separate service owns
  tool policy, approvals, and short-lived delegation. It gives stronger process
  isolation at materially higher deployment and policy-synchronization cost.

I recommend Option 2 for the current native single-control-plane deployment. It
closes the dangerous workaround, reuses the route-level permissions already in
production, and retains a clean migration path to Option 3 if the agent trust
model becomes multi-tenant or adversarial.

## Evidence

I inspected the relevant callers and boundaries rather than inferring them from
table names alone. `E04` and `E05` most influenced the recommendation: the
control plane already owns a live, granular permission decision, so duplicating
that decision at the worker or in a gateway today would make policy drift more
likely.

| Evidence | Finding or document | What it establishes |
| --- | --- | --- |
| `E01` | Documented authentication and node-token boundary | Automation keys are deferred; node tokens are encrypted but powerful. |
| `E02` | Reserved API-key schema | A natural migration point exists, but it lacks capability and lifecycle fields. |
| `E03` | JWT-only request authentication | Machine credentials need an explicit authentication path and must not inherit browser ticket behavior. |
| `E04` | Live server authorization | User permissions are refreshed per request, while admins currently bypass server checks. |
| `E05` | Route-level permission map | Existing server routes already distinguish view, console, power leaves, files, mods, backups, players, tasks, and settings. |
| `E06` | User-only audit attribution | Current audit data cannot identify which machine key acted. |
| `E07` | Reversible control-plane node secret storage | The API holds the worker's full-authority credential and should remain its only distributor. |
| `E08` | Shared node-token enforcement | Direct worker access has no per-user, per-server, or per-action policy. |

## Current Design And Failure Mode

Observed: humans authenticate to the API with short-lived JWTs, and each server
route evaluates a permission before the API calls the worker with a node token.
Observed: the worker sees only that shared token, so possession grants access to
all of its mounted operations. Observed: no external machine credential is
accepted by the API.

Inferred: an operator who wants an AI or automation agent to act today must
either share a browser credential or share the node token. A browser token is
short lived and carries ambient user authority; a node token is long lived and
bypasses user RBAC entirely. Neither choice has a separately revocable identity,
scope, server allowlist, or audit actor. The structural problem is therefore not
token generation alone. It is the absence of a first-class machine principal at
the boundary that already owns authorization.

## Desired Invariants

- The host worker's node token never leaves the control plane or machine-local
  deployment configuration.
- A machine request is authorized only when its key is active and unexpired,
  the target server is allowlisted, the key scope permits the route, and the
  owning user still has that permission.
- Global-admin ownership never bypasses a key's own capability bounds.
- Machine keys cannot access authentication, ticket, session, user, node,
  integration-secret, notification, global-audit, or key-management routes.
- Raw key material is returned once, never stored, never logged, and never
  accepted through a query string.
- Revocation, rotation, user deletion/demotion, and server-membership changes
  take effect on the next request without waiting for a cache or JWT expiry.
- Every audited machine action records both the human owner and the exact key.
- Key creation and rotation require human JWT authentication, current password,
  and a current TOTP code when MFA is enabled.
- The default key is narrow: explicit servers, explicit scopes, and an expiry no
  more than 90 days away.

## Constraints And Non-Goals

We preserve the SQLite store, existing deployment topology, REST surface, and
API-to-worker protocol. The initial design does not provide third-party OAuth,
mutual TLS client identity, human-in-the-loop approvals, tenant isolation, or a
new MCP transport. It authorizes trusted operator-controlled clients; it does
not make arbitrary agent-generated commands safe. Command and file-write
authority remain dangerous capabilities and must be granted deliberately.

## Before Architecture

[Before diagram](../diagrams/capability-access-before.mmd)

The important edge is that authorization terminates at the API. The worker only
knows its shared node secret. Adding external clients at the worker would move
them around the strongest policy and audit boundary rather than through it.

## Options

### Option 1: Direct node-token distribution

The strongest case for this baseline is compatibility: an external client can
call the worker immediately, with no API migration or additional SQLite lookup.
That simplicity is also its fatal security property. The token confers every
worker capability on the node, revocation disrupts the control plane itself,
and the worker cannot determine which user or automation acted. Encryption at
rest in the API does not help once the secret is distributed.

[Option 1 diagram](../diagrams/capability-access-direct-node-after.mmd)

| Change | Before | After | Security consequence | Cost |
| --- | --- | --- | --- | --- |
| External credential | None | Shared node token | Creates a full-authority bypass around user RBAC | Minimal code, severe blast radius |
| Revocation | Not applicable | Rotate node token everywhere | Coarse and operationally disruptive | Worker/API coordination |
| Attribution | Human user only | Indistinguishable node caller | No per-agent accountability | Incident response ambiguity |

This option has negligible CPU and memory cost, but its reliability story is
poor: revoking one compromised client also breaks API-to-worker operations. A
rollback is simply stopping distribution and rotating the node token, which is
exactly the disruptive recovery we should avoid.

### Option 2: Scoped control-plane access keys

This option makes automation a restricted credential inside the component that
already knows users, server membership, permissions, and audit context. A
256-bit random token with an identifying prefix is stored only as SHA-256. Its
row contains an explicit server allowlist, normalized permission scopes,
mandatory expiry, revocation state, and bounded usage metadata. The raw token is
shown once at creation or rotation.

Authentication creates both ordinary user claims and a machine-principal
context. A boundary middleware rejects machine principals outside the
`/api/v1/servers` subtree. Server authorization then checks the key's server and
scope capability before the existing user/admin decision. That order matters:
an admin-owned key is still bound by the key. Server listing is filtered to the
intersection rather than exposing names of other servers. Existing per-route
permissions continue to determine what `view`, `files.read`, `power.restart`,
or other scopes mean, reducing future policy drift.

[Option 2 diagram](../diagrams/capability-access-scoped-keys-after.mmd)

| Change | Before | After | Security consequence | Cost |
| --- | --- | --- | --- | --- |
| Machine identity | None | Hashed, named, expiring key | Separately revocable principal; raw secret absent from DB | One key lookup per request |
| Authorization | User RBAC only | Key capability AND live user RBAC | Least privilege survives admin ownership and later demotion | Authorization context and tests |
| Route exposure | All JWT routes | Machine keys limited to server subtree | Blocks account/global administration with a stolen key | Explicit boundary middleware |
| Audit | `user_id` | `user_id` plus `api_key_id` | Identifies the responsible automation credential | Migration and UI field |
| Lifecycle | Browser session controls | Human-only create/list/rotate/revoke with re-auth | Limits session-hijack persistence and supports recovery | Security UI and operational docs |

The critical path gains one indexed hash lookup and a few in-memory membership
checks. SQLite write amplification is contained by updating `last_used_at` and
`last_used_ip` only when stale rather than on every call. Memory stays bounded
by the size of one key's server/scope arrays. Availability remains coupled to
the existing API and SQLite—no new service fails—but malformed key metadata or
database errors must fail closed with a uniform 401/403 response.

Migration is additive. Existing JWT and worker-token protocols remain intact;
old reserved API-key rows have empty capabilities and must be unusable. Rollback
disables machine-key authentication and revokes issued rows before reverting
the additive code. Database columns can remain harmlessly in place during an
emergency rollback.

### Option 3: Isolated automation or MCP gateway

The attractive part of a gateway is that it can treat model-driven execution as
a distinct trust domain. It can expose curated tools, require approvals for
destructive actions, keep long-lived credentials out of model runtimes, issue
short-lived delegated capabilities, and isolate parser or transport flaws from
the control plane. This becomes compelling for third-party agents or multiple
tenants.

[Option 3 diagram](../diagrams/capability-access-gateway-after.mmd)

| Change | Before | After | Security consequence | Cost |
| --- | --- | --- | --- | --- |
| Process boundary | API only | Gateway plus API | Better isolation and policy mediation | New service, secrets, health and deployment |
| Policy ownership | API route permissions | Gateway tool policy plus API RBAC | Can add approvals; risks policy mismatch | Versioned delegation protocol required |
| Credential lifetime | JWT/node token | Gateway identity and short-lived delegation | Narrows credential exposure | Issuer/verification and clock handling |
| Observability | API audit | Tool-call plus API audit | Better intent-level evidence | Correlation IDs and dual retention |

We should be honest about the operational mechanism behind the cost: every
action gains a network hop and serialization boundary; every deployment gains a
new availability dependency; and permissions must be expressed at both the tool
and resource layers without contradictions. A rollback needs a direct scoped
API credential path anyway. I would choose this option once approval workflows,
untrusted tenants, or a standardized MCP surface are requirements, but not only
to authenticate the first trusted automation client.

## Comparison

| Dimension | Option 1: node token | Option 2: scoped API key | Option 3: gateway |
| --- | --- | --- | --- |
| Security | Regresses: node-wide ambient authority | Improves: explicit capability intersected with live RBAC | Improves most for untrusted runtimes; new gateway TCB |
| Performance | Neutral; direct worker call | Small indexed lookup; no new hop | Extra hop, serialization, and policy evaluation |
| Memory | Neutral | Bounded per-request scope/server data | New process, connection pools, and policy state |
| Reliability | Regresses during compromise/rotation | Similar to current API; fail-closed DB dependency | New failure domain and retry/backpressure design |
| Operability | Superficially simple, hard to investigate | Key inventory, rotation, audit, and clear runbook | Separate deployment, health, logs, upgrades, and correlation |
| Migration | Immediate but unsafe | Additive schema/auth/UI; existing protocols unchanged | New protocol/service plus dual-policy migration |

No performance claim above is measured. Validation should compare JWT and key
authentication latency under representative status polling and verify that the
stale-usage update avoids sustained SQLite writes. A gateway benchmark is
deferred until that option has concrete transport and workload requirements.

## Recommendation

I recommend Option 2 under the current constraints. It centralizes machine
authorization at the existing policy owner, adds no deployment unit, and
directly prevents node-token distribution from becoming normal practice. The
principal residual risk is that a granted capability remains powerful: a key
with `console`, `files.write`, or `power.kill` can do real damage within its
allowlisted servers. Clear UI language, narrow defaults, short expiry, and
separate diagnosis/action keys are therefore part of the security control, not
optional polish.

Option 3 should replace or front Option 2 if external clients are not fully
operator controlled, if actions need approval, or if a model-facing tool schema
must be centrally curated. Option 1 should never be documented as supported.

## Evidence Coverage And Residual Risk

| Evidence | Effect of Option 2 | Tactical protection still required |
| --- | --- | --- |
| `E01` — documented auth boundary | Addresses the declared automation gap | Keep node-token guidance explicit |
| `E02` — reserved key schema | Addresses with capability/lifecycle migration | Reject legacy empty-capability rows |
| `E03` — JWT-only auth | Addresses with distinct bearer prefix and principal | Never accept keys from query strings or issue tickets to them |
| `E04` — live server authorization | Mitigates by intersecting key and live user access | Evaluate key bounds before admin bypass |
| `E05` — route permission map | Reuses and strengthens existing gates | Add regression coverage for every mounted permission class |
| `E06` — user-only audit | Addresses with `api_key_id` attribution | Preserve user attribution for accountability chain |
| `E07` — reversible node secret | Mitigates pressure to distribute it | Keep it internal and encrypted at rest |
| `E08` — worker shared token | Unaffected internally | TLS and loopback/private exposure controls remain mandatory |

Residual risks include client-side secret theft, overbroad scopes chosen by an
operator, dangerous but authorized console commands, API-process compromise,
and traffic analysis. The design narrows and attributes authority; it does not
sandbox the external agent or validate the intent of arbitrary commands.

## Migration And Rollout

The migration is additive and fail-closed. We add key metadata and audit
attribution, then ship store/auth tests, route authorization, handlers, UI, and
docs as one compatible release. Existing empty reserved rows authenticate as
nothing. Production rollout should begin with a short-lived read-only diagnosis
key for one non-critical server. After read-path and audit verification, create
a separate narrowly scoped action key. Rotation is tested before broader use.

Rollback first revokes all issued keys and disables the machine authentication
branch. Existing JWT users and API-to-worker credentials continue unchanged.
The additive columns may safely remain until a later maintenance migration.

## Validation Plan

- Prove raw tokens never appear in SQLite, JSON lists, logs, audit detail, or URL
  query strings; verify only the creation/rotation response exposes the secret.
- Exercise invalid, malformed, expired, revoked, rotated, deleted-user, and
  database-error credentials; all must fail closed without existence leaks.
- Verify an admin-owned key cannot escape its server allowlist or scope, while a
  later user demotion or permission removal takes effect on the next request.
- Verify key requests cannot reach non-server routes or mint download/WS
  tickets, and that server lists reveal only allowlisted and currently
  authorized servers.
- Verify every audited machine mutation records the user and key ID.
- Race rotation/revocation against requests and confirm the old token stops
  working immediately after the committed update.
- Benchmark JWT versus key-authenticated status polling and observe SQLite write
  rate for sustained polling; reject a design that writes on every request.
- Run API tests, migration tests, web typecheck/lint/build, and the repository
  test target. Perform a TLS-backed smoke test with a read-only key.

## Implementation Work Packages

The ordered, acceptance-testable work is recorded in
[the selected implementation plan](../implementation/scoped-control-plane-keys.md).

## Open Questions

- Should production policy lower the maximum lifetime below the proposed 90
  days? The code should expose one bounded constant so this is easy to tighten.
- Which first external clients require WebSocket console streaming? Header-based
  authentication is supported; key-to-query-ticket conversion is deliberately
  excluded.
- When an MCP transport is needed, should it be an adapter operated alongside
  the client or the isolated gateway in Option 3? That choice depends on the
  trust placed in client-side agent code.

