# Security Hardening Review: Authorized Agent Access

## Evidence Basis

I inspected the current control-plane authentication, server authorization,
audit, node-secret, and host-worker boundaries at revision
`f27626beb3e383f22e4f7b75137701097a916615`. The evidence inventory is recorded
in [context.md](context.md). This is a source-derived design review; it is not a
claim that the proposed control exists yet or that an incident occurred.

The current architecture deliberately has no machine credential. That protects
the API from accidental ambient automation access, but it leaves operators with
one tempting workaround: distribute the host worker's shared node token. That
credential bypasses control-plane RBAC, has node-wide authority, and cannot
attribute actions to an external agent. We can avoid that failure mode by making
automation a first-class control-plane principal.

## Constraints

- Preserve the native, single-control-plane deployment and current Go/SQLite
  stack; no new service or dependency without a demonstrated need.
- Keep node bearer tokens internal to the API and preserve the existing
  API-to-worker wire protocol.
- Reuse live user and per-server authorization so demotion and membership
  changes remain immediately effective.
- Support controlled diagnosis and actions without granting account, node,
  user, integration-secret, or key-management authority to machine keys.
- Assume balanced security and engineering cost. No measured throughput budget
  or machine-key concurrency target was supplied.

## Opportunity Portfolio

| Opportunity | Evidence | Options | Recommendation | Proposal |
| --- | --- | --- | --- | --- |
| Make external automation a bounded, attributable principal | Reserved API-key schema, JWT-only auth, live server RBAC, user-only audit, shared node token (`E01`–`E08`) | 1. Direct node-token distribution; 2. scoped control-plane access keys; 3. isolated automation/MCP gateway | Implement Option 2 now; keep Option 3 as the isolation path if untrusted tenants or approval workflows arrive | [Capability-scoped agent access](proposals/capability-agent-access.md) |

## Recommendation Summary

I recommend Option 2 under the repository's current single-host constraints:
hashed, expiring, revocable access keys bound to an explicit set of servers and
fine-grained scopes. Every request must satisfy the key's capability and the
owning user's permissions as they exist at request time. Machine credentials
are accepted only on the existing `/api/v1/servers` surface, so they cannot mint
tickets, manage accounts, alter nodes, read integration secrets, or administer
other keys. Existing route permissions remain the enforcement point, but key
restrictions are evaluated before the global-admin bypass.

This option adds no network hop and preserves the worker protocol. It does add
security-sensitive code to the API process, so the implementation plan requires
one-time secret display, password plus enabled-MFA reauthentication, bounded
expiry, soft revocation, rotation, per-key rate-limit identity, server-list
filtering, and audit entries that name both the owning user and key.

Option 3 becomes preferable if ServerManager must host mutually untrusted agent
tenants, require interactive approval before destructive tools, issue
minute-lived delegated tokens, or expose a standardized MCP transport. Those
requirements justify an independently deployable policy and isolation boundary;
they are not present in the current source or request.

## Next Decisions

- Implement the selected scoped-key design according to
  [the implementation plan](implementation/scoped-control-plane-keys.md).
- During rollout, create separate read-only diagnosis and action keys rather
  than one broad credential, and require TLS at the public reverse proxy.
- Revisit the gateway option before allowing third-party or tenant-controlled
  agents, interactive approvals, or cross-control-plane federation.

