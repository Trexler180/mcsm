# Security Hardening Review: Remote Agent Access Without a Local Connector

## Evidence Basis

I inspected the control-plane authentication boundary, the freshly landed
scoped access-key foundation, the server authorization intersection, the audit
attribution path, the rate-limit identity, and the diagnostic data sources that
any model-facing tool would have to read. The evidence inventory, the exact
revision, and the working-tree drift are recorded in [context.md](context.md).

This is a source-derived design review. It does not claim an incident occurred,
and it does not claim the proposed controls already exist.

## The Problem

The access-key phase gave ServerManager a real machine principal. It did not
give a *user* a good way to hand that authority to an agent they already run.
Today the only path is: open Account → Security, re-authenticate, mint a
90-day `mcsm_pat_…`, and paste it into an agent configuration — which in
practice means pasting a reusable, long-lived, server-mutating secret into a
chat window, a shell history, a dotfile, or all three. The credential is
bearer-only, so anything that can read it can replay it until it expires or
someone remembers to revoke it.

The user's constraint is specific and worth taking literally: **no local
connector may be installed, and no reusable API key may be pasted into a chat.**
That rules out the two easy answers (a stdio MCP shim on the operator's box, and
"just use the PAT"), and it points at the one thing the panel already is — a
web application with a live, MFA-capable browser session that can host an
interactive consent screen.

## Constraints

- Preserve the native single-control-plane deployment, the Go/SQLite stack, and
  the existing API-to-worker protocol. Node bearer tokens stay inside the API.
- No local install on the operator's machine, and no modification of the user's
  global Claude/Codex configuration during development or demo.
- Reuse the live user/per-server permission lattice so a demotion, a membership
  change, or a server removal lands on the agent's very next tool call.
- Reuse the existing login/MFA/session machinery for consent without weakening
  it. Consent must fail closed.
- Interoperate with MCP clients that negotiate older protocol versions while the
  server speaks a current one.
- Assume a balanced security/engineering profile. No throughput target, session
  count, or tool-call latency budget was supplied.

## Opportunity Portfolio

| Opportunity | Evidence | Options | Recommendation | Proposal |
| --- | --- | --- | --- | --- |
| Let an existing agent reach ServerManager with delegated, revocable, human-approved authority | Credential boundary, scoped keys, machine intersection, route map, step-up re-auth, audit, rate identity, untrusted log text (`E01`–`E12`) | 1. Remote MCP authenticated by a copied PAT; 2. embedded remote Streamable HTTP MCP with OAuth browser consent; 3. local sidecar / device pairing | Implement Option 2 now; keep Option 3 as the path if a future client cannot do remote OAuth at all | [Remote MCP control plane](proposals/remote-mcp-control-plane.md) |

## Recommendation Summary

I recommend **Option 2: an embedded remote MCP server inside the existing Go
API, protected by an OAuth 2.1 authorization-code flow with browser consent.**

The MCP endpoint is a narrow tool facade — never a REST proxy. It exposes five
bounded diagnostic reads and one action *request* tool, and nothing else. The
authority behind any tool call is the intersection

```
active OAuth grant ∩ unexpired, audience-bound access token
                  ∩ approved server allowlist ∩ approved MCP scopes
                  ∩ the owner's live ServerManager RBAC
```

Every term is re-evaluated per call. There is no cache, because immediate
revocation is a required invariant and a cache is exactly a window in which a
revoked grant still works.

This wins on the user's own terms:

- **No install.** The client speaks Streamable HTTP to a URL the panel already
  serves. `claude mcp add --transport http …` and a five-line Codex entry are
  the entire setup.
- **No pasted secret.** The user runs one command, a browser opens on the panel
  they are already signed into, they read exactly what is being granted, and
  they click Authorize. The client receives a short-lived, audience-bound access
  token it can refresh; the human never sees or handles token material.
- **Real revocation.** A grant is a first-class row with an owner, an expiry,
  and a revoke button. Revoking it kills the access token, the refresh family,
  and every future tool call at once.

It also costs something honest: an OAuth authorization server is
security-critical code living in the API process, and getting PKCE, exact
redirect matching, resource binding, code single-use, refresh rotation, and
consent CSRF right is the whole job. The implementation plan treats each of
those as a named work package with its own tests rather than as prose.

**Actions are not delegated.** `start`, `stop`, and `restart` are the only
mutating capabilities, and the agent cannot perform them — it can only *file a
request*. A human approves it in the dashboard, with password and TOTP step-up,
after which the server re-checks the owner's live permission and the grant, and
executes at most once under an atomic state transition. Trusting the MCP host's
own "allow this tool?" prompt would put the security boundary inside software
the panel does not control; this keeps it on the panel's side of the wire.

Option 1 (PAT-authenticated remote MCP) is the baseline. It is genuinely
simpler — the bearer path already exists — and it is a defensible fallback if a
client's OAuth support turns out to be broken. But it reintroduces exactly the
copy-a-long-lived-secret UX the user asked to avoid, it gives no per-client
identity beyond a key name, and it makes consent a checkbox in a form rather
than a decision made against a live authenticated session. It stays implemented
only as a **disabled-by-default** fallback.

Option 3 (local sidecar or device pairing) is cryptographically the strongest
— a device-bound key never leaves the machine, and there is no browser redirect
to get wrong — but it requires installing and updating software on the
operator's box, which the request rules out. It is recorded, with its
trade-offs, so a future phase can pick it up if remote OAuth proves
uninteroperable.

## Next Decisions

- Implement the selected design per
  [the implementation plan](implementation/embedded-oauth-mcp.md).
- Keep the tool surface at diagnosis + action-request until an operator has run
  a full grant → diagnose → request → approve → execute → revoke cycle.
- Require an explicit configured public origin (`MCP_PUBLIC_ORIGIN`, falling
  back to `APP_ORIGIN`) in any non-loopback deployment, so canonical resource
  identifiers and redirect matching cannot be steered by a `Host` header.
- Revisit device-bound pairing if a client appears that cannot complete a
  remote authorization-code flow, or if ServerManager ever hosts mutually
  untrusted agent tenants.
