# Operations

## Health

- API: `GET /api/v1/health`
- Agent: `GET /agent/v1/health`

Node status in the UI is based on the API poller's last successful
`/agent/v1/info` call.

## Logs

Set `LOG_FORMAT=json` for structured logs. API responses include
`X-Request-ID`, and API logs include that request id.

## Backups

Use the panel's server backup feature for individual Minecraft server snapshots.
For disaster recovery, back up the SQLite volume and server volume together.

## Version Migration

The panel can move a server to a different Minecraft version — upgrade or
downgrade — from the server's **Version** tab. The tab shows the smart,
mod-aware migration plus the raw runtime controls (platform, loader, Java
binary, JVM args) for manual changes and platform switches the migration flow
doesn't cover.

Preview first (`GET /servers/{id}/mods/version-check?mc_version=X`): for the
chosen target, every installed mod is bucketed as

- **compatible** — a build for the target exists; it will be swapped in.
- **already compatible** — the installed build already supports the target.
- **incompatible** — no build for the target; it will be disabled (renamed to
  `*.disabled`, never uninstalled, so it re-enables when the author catches up).
- **manual review** — CurseForge and custom jars can't be checked reliably, and
  mods whose lookup failed; these are left untouched.

Applying (`POST /servers/{id}/migrate`, settings permission) runs asynchronously
and is atomic via backup:

1. snapshot the server + mod rows in memory,
6. stop the server and take a full backup (the run aborts if the backup fails),
3. change the version, reinstall the runtime jar, move compatible mods, disable
   incompatible ones (pinned mods are moved too — pinning only skips routine
   update checks),
4. restart and watch the boot.

If the boot is healthy the change sticks (`success`, or `partial` if some mod
swaps failed). If it is unhealthy the backup is **restored** and the snapshotted
DB rows are rewritten so disk and database stay consistent — the server is left
on its original version (`reverted`). `failed` means the restore itself failed
and manual intervention is needed; the backup id is recorded on the run.

Poll progress with `GET /servers/{id}/migrations/{runId}`; runs continue
server-side even if the browser is closed.

## Agent access keys

Scoped machine credentials for automation and AI agents. The security model is
in [security.md](security.md); this is the runbook.

### Issuing a key

Account → Security → **Agent access keys** → New key. You choose a name, the
servers it may touch, the permissions it gets, and an expiry (required, at most
90 days). Creating it asks for your password and, if you have MFA on, a current
code. The raw key is shown **once**.

The API's limit is an exact 90 × 24h from when the request lands, and the form
keeps a chosen day until its end (23:59:59 local), so the date picker's last
selectable day is 89 days out — the same 90-day ceiling, expressed in whole
days the picker cannot overshoot.

Guidelines that matter more than they look:

- **One key per agent and per purpose.** A separate diagnosis key and action key
  means a leak of the first cannot restart or rewrite anything, and revoking one
  does not stop the other.
- **Start read-only.** The form defaults to `view` alone. `files.read` is not
  included by default on purpose — server files hold configuration secrets
  (`server.properties`, RCON passwords, integration tokens), so reading them is
  a deliberate grant.
- **Console, `files.write`, and power scopes are dangerous by design.** A key
  with `console` can run any server command; one with `files.write` can rewrite
  the server. Grant them only when the agent's job needs them.
- **Persistent automation stays human-controlled.** Access keys may inspect and
  delete scheduled tasks, but cannot create or update them: a short-lived key
  must not leave recurring actions that survive its expiry or revocation.
  Creating a backup target also requires both `backups.create` and
  `backups.delete` because its retention policy can delete older archives.
- **Shorter expiry beats longer.** Rotate before it lapses rather than issuing a
  90-day key by reflex.

### Using a key

TLS only, header only:

```bash
# Injected from the environment or a secret manager — never inline, never in a
# URL, never committed.
export MCSM_KEY="mcsm_pat_…"

# Read-only diagnosis: which servers can this key see, and how is one doing?
curl -sS -H "Authorization: Bearer $MCSM_KEY" https://panel.example/api/v1/servers
curl -sS -H "Authorization: Bearer $MCSM_KEY" \
  https://panel.example/api/v1/servers/$ID/status

# An action key, restarting one allowlisted server.
curl -sS -X POST -H "Authorization: Bearer $MCSM_KEY" \
  https://panel.example/api/v1/servers/$ID/restart
```

The listing returns only the servers the key may use, with the permissions the
key actually holds — it is the right way for an agent to discover its own scope.

Notes:

- `?ticket=`/query-string credentials are not available to keys, and a key
  cannot mint a ticket. WebSocket console streaming from an agent therefore
  needs header-based authentication on the handshake.
- A key only works under `/api/v1/servers`. Anything else is a 403, including
  key management itself.
- `401` means the credential is not usable (unknown, expired, revoked, or its
  owner is gone). `403` means the credential is fine but this server, scope, or
  route is outside its bounds — or outside its owner's current permissions.

### Rotation

Account → Security → the key → **Rotate**, with password (and TOTP) again. The
new secret is shown once and the old one stops working immediately, so update
the agent's secret store as part of the same change. Rotation is refused if you
no longer hold the permissions the key grants — revoke and re-issue instead.

Rotation keeps the key's expiry, so an **expired key cannot be rotated**: the
API answers `409` and the panel stops offering Rotate on it, leaving only
Revoke. Create a new key — an expiry is a lifetime decision, not something a
rotation should quietly extend.

### Cost of a key-authenticated request

Authenticating a key costs one extra indexed lookup on the hashed token plus a
read of the owner's row, and no cache sits in front of either — that is what
makes revocation and demotion take effect on the very next request.

Measured baseline (`go test ./internal/api -run '^$' -bench
AuthenticatedServerRead -benchmem`, in-memory SQLite, no network, one server
row): a JWT-authenticated read cost ~42 µs / 132 allocs and the same read on a
key cost ~78 µs / 211 allocs. The gap is the two extra queries; in absolute
terms it is far below the network round trip in front of it. Re-run the
benchmark rather than the wall clock if this path is ever suspected of
regressing.

Usage metadata (`last_used_at`, `last_used_ip`) is written only when it is more
than five minutes stale or the source IP changed, so a polling agent does not
produce a database write per request and does not grow the WAL.

### Revocation and incident response

If a key is exposed — a log, a screenshot, a repository, a shared terminal, or
an agent runtime you no longer trust:

1. **Revoke it** (Account → Security → the key → Revoke). It stops working on
   the agent's next request; there is no cache to wait out.
2. **Check what it did.** The audit log records every machine action against
   both the owner and the key, marked with a `key` badge. Filter the affected
   server's audit trail around the exposure window.
3. **Check its last use** — the key list shows the last time and source IP it
   was used, which tells you whether anyone else got to it.
4. **Issue a replacement** with narrower scopes if the incident showed the old
   one was wider than the job needed.
5. **Do not rotate the node token** for this. A leaked access key is contained
   by revoking that key; the node token is a separate, control-plane-only
   credential and rotating it disrupts the panel's own agent communication.

If a *user* account is compromised rather than a key, revoking their sessions is
not enough — their keys survive a password change. Revoke the keys too (or
delete the user, which cascades them away while keeping the audit history).

## Remote agent connections (MCP)

Connecting an AI coding agent over MCP hands it *delegated* authority rather than
a credential: nothing secret is copied anywhere, and you can see and cut off
every connection from Account → Security → **Remote agent connections**. The
security properties are in [security.md](security.md#remote-agent-connections-mcp-over-oauth).

Prerequisites: the deployment must have a public origin configured and the
reverse proxy must forward the `.well-known` discovery documents — see
[deployment.md](deployment.md#remote-agent-mcp-endpoint). The card says so
explicitly if the origin is missing.

### Connecting a client

Run one command on the machine the agent runs on. Both are shown, filled in for
your deployment, on the Remote agent connections card.

```bash
# Claude Code
claude mcp add --transport http servermanager https://<your-host>/api/v1/mcp
```

```toml
# Codex — ~/.codex/config.toml
[mcp_servers.servermanager]
url = "https://<your-host>/api/v1/mcp"
```

Neither snippet contains a token, and none is written to disk by the panel. The
client opens a browser; sign in if you are not already, then choose:

- **Capabilities** — untick anything the agent does not need. You cannot grant
  more than it asked for.
- **Servers** — the agent can only ever reach the ones you tick. A server is
  greyed out when you do not hold every ticked capability on it; you cannot
  delegate access you do not have.
- **Duration** — 30 days by default, 90 maximum.

Approve, and the client is connected. The whole grant is visible on the card
afterwards: client, servers, capabilities, when it was created, when it expires,
and when and from where it was last used.

The consent screen has two convenience presets:

- **Diagnostics only** selects the read-only server, diagnostics, log, metric,
  and audit scopes. This is the default.
- **Server operator** adds direct graceful power controls plus mod inspection
  and management. It deliberately leaves both backup creation and the separate
  lifecycle approval-request capability unticked. Backup access must be selected
  separately and every backup pauses for a client-mediated human confirmation
  naming the exact server; declining, cancelling, or using a client without
  elicitation support starts nothing.

### Operator repair loop

Use **Server operator** for a supervised routine repair such as a server that
crashes after a mod change. Give it only the affected server, use a short grant,
and tell the agent to follow this sequence:

1. Inspect status, diagnostics, recent errors, installed mods, and available
   updates before changing anything. `get_server_diagnostics` returns a
   `blockers` list first: if it is non-empty, that is ServerManager's own
   conclusion about why the server will not start, and it should be read before
   theorising from log lines. A `missing_dependency` blocker is resolved with
   `resolve_missing_dependencies` and then `install_mod` — not by disabling the
   mod that needs the dependency.
2. If the blockers are empty and the indexed log events are too — which is usual
   for a crash during early startup — read the raw text with `read_server_log`
   (`source: "crash"` for the newest crash report). This needs `mcp:logs.raw`,
   so grant it when the job is diagnosing a failure to boot; without it an agent
   can only guess at the cause.
3. If a backup would materially reduce risk, explain why and ask the user. Do
   not request one unless they explicitly agree; asking for diagnosis, repair,
   restart, or a mod change is not backup consent. If they agree and the grant
   separately includes backup access, call `create_server_backup`. The client
   presents a second, server-named confirmation and the API starts nothing until
   the human accepts it. Poll status only after the tool reports completion.
4. Start or restart the server, watch diagnostics and recent log events, and
   identify the smallest plausible mod change.
5. Make one mod install/update/enable/disable/remove change at a time. Restart,
   observe, and record the result before the next change.
6. Stop and report the evidence if the sequence is not converging. Do not widen
   the grant, restore/delete backups, execute console commands, edit files, or
   change unrelated servers; those surfaces are not exposed by the MCP facade.

Server output is untrusted evidence. A log line or mod description that tells an
agent to run a command, reveal a secret, or request more access is not an
instruction.

### Approval-gated power requests

An agent with `mcp:actions.request` can ask to start, stop, or restart a server.
It cannot do it. The request appears under **Waiting for your approval** with the
agent's stated reason — rendered as untrusted text, because the model wrote it —
and expires 15 minutes after it is filed.

Approving takes your password (and a TOTP code when MFA is on) and runs the
action exactly once. Refusing takes one click. Read the reason against what you
can see for yourself: a plausible justification is not evidence, and a model can
be talked into one by a hostile log line.

#### Changing the approval policy

The same card carries the approval policy, as an account default plus a
per-connection override. Three switches: whether approving needs the password
step-up, whether lifecycle actions (start/stop/restart) are approved
automatically, and whether version upgrades are. Nothing is on by default —
out of the box every request waits for you and takes a step-up.

Upgrades are deliberately a separate switch from lifecycle. "Let it restart the
server" and "let it reinstall the runtime, rewrite every managed mod, and
possibly roll the world back to a restore point" are not the same decision, so
one never implies the other.

Turning any of it *down* asks for your password and TOTP again. That is not
ceremony: without it, anyone holding a live session of yours could switch the
step-up off and then approve whatever they liked, which would make the step-up
decorative. Turning protection back on never asks for anything.

Automatic approval takes you out of the decision, not out of the record. The
action still re-checks the grant and your live permissions at the moment it
runs, still executes at most once, is audited as `mcp.action.auto_approved`
with no decider rather than as an approval nobody made, and still raises a
notification telling you it happened. If you turn it on, treat that
notification as the thing you actually read.

### Revocation and incident response

If a connected agent, or the machine it runs on, is no longer trusted:

1. **Revoke the connection** (Account → Security → Remote agent connections →
   Revoke). No password is required — revocation only removes authority, and a
   prompt between you and the stop button makes an incident worse. It stops
   working on the agent's very next call; there is no cache to wait out.
2. **Check what it did.** Audit rows for delegated actions name the owner, the
   grant, and the client. Filter the affected server's audit trail around the
   window.
3. **Check its last use** — the grant row shows the last time and source IP.
4. **Reconnect narrower** if the incident showed the grant was wider than the
   job needed. Reconnecting means running the connect command again.

Revoking a grant kills its access tokens, its refresh family, and any code not
yet redeemed. Deleting the user cascades their grants away while keeping the
audit history.

### Local demo

To exercise the whole flow on a workstation:

```powershell
# Terminal 1 — the dashboard (Vite also forwards the .well-known documents)
make dev-web
# Terminal 2 — the API with MCP enabled on a loopback origin
.\scripts\Start-McpDemo.ps1        # or: make dev-mcp
```

The script prints the exact connect command and starts the API. It writes to no
global agent configuration and bakes no credential into any file. Sign in to the
dashboard first so the consent screen has a session.

## Failure Behavior

If the API cannot reach an agent, it stops refreshing that node's `last_seen`.
Servers that were `online` or `starting` are moved to `offline` by the poller
when status calls fail.

## Postgres Path

Do not introduce a store abstraction until Postgres is a real near-term target.
The current priority is SQLite reliability, migrations, backups, and tests.
