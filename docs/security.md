# Security

## Auth Model

- **Global admins** manage nodes, users, app settings, the global audit log, and
  every server. Creating servers and changing start-command inputs is admin-only
  (see below).
- **Server owners and collaborators** act on individual servers through a
  granular per-server permission system (`server_members` /
  `server_permissions`): view, console, power (start/stop/restart/kill),
  settings, files (read/write/delete), mods (install/update/remove), backups
  (create/restore/delete), players
  (whitelist/kick/ban/op/inspect/delete), and tasks. Each route is gated on the
  specific permission, and the global admin role is re-read from the database on
  every check so a demotion takes effect immediately.

  `players.inspect` covers reading a player's saved data — inventory, ender
  chest, coordinates, health and visit history. It is deliberately separate from
  the rest of the group: knowing who plays on a server is a much smaller
  disclosure than reading what they carry and where they are, so a helper who
  manages the whitelist does not get that by default.

The granular permission model is live (not reserved). `api_keys` now backs agent
access keys (see below).

The global roles are `admin` and `user`. The former `operator` global role,
which was accepted but granted nothing, is gone (migration 031 turned existing
operators into users); "operator" is now a per-server preset. The permission
groups, their leaves and the administrator/operator/viewer presets are served as
data at `GET /api/v1/permissions/presets`, so the dashboard's access editor and
the API share one definition, and adding a member may name a preset instead of
a list.

### Server folders

Folders (`server_folders`, `servers.folder_id`) are a flat, presentation-only
grouping — they grant no access of their own. Creating, renaming, and deleting
them is admin-only; moving a server between folders rides the server's existing
`settings` permission, like renaming it.

Listing is scoped rather than global: a non-admin is only shown folders that
contain at least one server they can already see, and the reported server count
covers only those servers, so a folder never leaks the existence of servers or
sibling folders the caller cannot reach. Deleting a folder is non-destructive —
the foreign key sets `folder_id` back to NULL, so its servers become ungrouped
rather than being deleted or stopped.

### Privileged start-command fields

`java_binary`, `jvm_args`, and `directory_path` are inputs the agent executes,
so changing them is effectively host code execution. They are editable by global
admins only — a server-scoped `settings` collaborator cannot change them — and
readable by admins only: everyone else receives them as `null`. Sending them back
unchanged is not a change, so a form that echoes the record still saves. The
`import` setting (which jar an imported server runs, and whether a runtime is
installed) is admin-only the same way, and every settings key is validated by
type. As defense in depth, the agent independently refuses to launch anything
whose basename is not a Java executable (`java`/`javaw`).

## Tokens

- JWT access tokens (15 min) authenticate ordinary API requests. Each one names
  the refresh-token session it was issued under (`sid`), and that session is
  checked on every request: logging out, revoking a session, changing or
  resetting a password, or a demotion ends its access tokens at once instead of
  up to 15 minutes later. Tokens carry and are checked for issuer
  (`mcsm-api`), audience (`mcsm-panel`) and type (`access`), so no other token
  the panel signs can pass as one. A rejected token says why
  (`token_expired`: refresh and retry; `session_revoked`: sign in again).
- Refresh tokens are stored only as hashed rows and rotated on use. A token
  presented again within 30 seconds of its rotation (two tabs refreshing
  together) still earns an access token; presented after that, it is treated
  as stolen and the whole session ends. Browser refresh tokens are `HttpOnly`,
  `SameSite=Strict` cookies scoped to `/api/v1/auth`; the frontend stores only
  the short-lived access token. Logout is keyed on that cookie and works with
  an expired access token.
- Query-string tokens are never accepted. WebSocket/download endpoints that
  cannot carry an Authorization header accept a short-lived, single-use ticket
  minted from an authenticated request instead. A ticket is bound to the one
  path it was minted for: a download ticket cannot open a console socket.
- Agent calls use bearer tokens stored **encrypted at rest** in the API database
  (AES-256-GCM under the app encryption key) and never emitted in node JSON. The
  agent compares the presented token in constant time. Changing a node's address
  requires entering its token again, so an address edit cannot send the stored
  token to a new host.
- Every encrypted value (node tokens, TOTP secrets, integration secrets,
  webhook signing secrets) is bound to its row as associated data, so a value
  copied onto another row does not decrypt. Values written before that are
  upgraded at startup. **Back up the encryption key with the database**
  (`APP_ENCRYPTION_KEY`, or `.mcsm-secret-key` beside it): without it, nodes
  are unreachable and two-factor accounts cannot sign in, which sign-in reports
  plainly (`encryption_key_mismatch`).

  **The node token is never distributed.** It grants the worker's entire HTTP
  surface on that host with no user, server, or action check, and revoking it
  breaks the control plane itself. Automation that needs to act on a server uses
  an agent access key, which goes through the API's authorization instead of
  around it. Do not hand a node token — or a user password, refresh token, or
  browser JWT — to an agent, a script, or a third party.

## Agent access keys

Operator-controlled automation and AI agents authenticate with a scoped access
key: `Authorization: Bearer mcsm_pat_<secret>`, over HTTPS, in the header only.

- **Bounded by construction.** A key is owned by one user and carries an
  explicit server allowlist, an explicit set of permission scopes drawn from the
  same vocabulary collaborators are granted, and a mandatory expiry no more than
  90 days out. There is no "all servers", no "all permissions", and no
  never-expires option.
- **Authorization is an intersection**, evaluated in this order:
  `active key ∩ unexpired key ∩ allowed route ∩ allowed server ∩ key scope ∩ the
  owner's live permissions`. Only the final term has a global-admin bypass — the
  key's own route, server, and scope terms never do. An admin-owned key that
  lists one server and one scope can still only do that one thing, and losing a
  permission or being demoted closes the key on the next request.
- **The `admin` server scope cannot be granted to a key.** That keeps membership
  administration, server deletion, and the create/import/clone paths out of
  reach of automation entirely, since those routes require the global admin role
  and a machine principal never satisfies it.
- **Route boundary.** A key is only accepted under `/api/v1/servers`. Every
  other route family — authentication, tickets, sessions, MFA, users, nodes,
  integration secrets, notifications, the global audit log, and key management
  itself — refuses it, so a stolen key cannot administer the panel or mint a
  second credential.
- **The raw secret is shown once**, in the create or rotate response. The
  database stores only its SHA-256 and a short non-secret display prefix
  (`mcsm_pat_AbCdEf12`) used to identify the key in the UI. It is never logged,
  never returned by a listing, and never accepted from a query string. A key
  cannot mint a download/WebSocket ticket, so there is no path from a key to a
  URL-borne credential.
- **Lifecycle is human-only.** Listing, creating, rotating, and revoking happen
  under an interactive sign-in (`/api/v1/auth/api-keys`); creating and rotating
  additionally require the current password and, when the account has MFA, a
  current TOTP code. Recovery codes are not accepted for key issuance. Failed
  reauthentication is throttled per IP and per account, and returns one generic
  error. Login and every step-up (key creation and rotation, MCP action
  approval, relaxing the approval policy) spend from one shared budget, so a
  stolen session cannot multiply its password guesses across endpoints. A key can only be issued with permissions its creator currently holds
  on the servers they select, so a key never widens its owner's authority.
- **Rotation and revocation are immediate.** Rotation swaps the stored hash in a
  single committed update, so the old secret dies with no overlap. Revocation is
  soft: the row survives so audit history keeps naming a real credential.
  Neither is cached — every request re-reads the key and the owner's role.
  A console or metrics WebSocket opened with a key re-checks the exact secret
  it presented on its periodic permission check, so revoking or rotating the
  key closes it.
  Rotation keeps every other field, expiry included, so an expired key cannot be
  rotated back to life: that is a 409, and the remedy is a new key. Revoking an
  expired key is still allowed.
- **Fail closed.** An unknown, expired, revoked, orphaned, or malformed key —
  and any store failure — is one uniform 401. Capability metadata is
  re-validated on read, so a row edited outside the API cannot widen a key.
- **Attribution.** Machine actions record both `user_id` (who owns the
  credential) and `api_key_id` (which credential acted); the audit UI marks
  those rows. Revoking a key does not erase its history.
- **Disclosure.** `GET /servers` for a key returns only the intersection of its
  allowlist with what its owner can currently reach, with the permissions
  reduced to what the key actually holds — the rest of the fleet is not named.
- **Rate limiting** is keyed per key, not per owner, so a noisy agent cannot
  spend its owner's or another key's budget.

Operational guidance — issuing, rotating, and responding to a leak — is in
[operations.md](operations.md).

## Remote agent connections (MCP over OAuth)

An AI coding agent (Claude Code, Codex) can connect to the panel over the Model
Context Protocol without ever being handed a credential. The panel embeds an
OAuth 2.1 authorization server and a Streamable HTTP MCP resource; connecting is
one command plus a browser approval, and the token goes straight from the token
endpoint to the client.

This exists **instead of** giving an agent an access key. A key is long-lived and
ends up in a config file or a dotfiles repository; a grant is delegated authority
the operator can see and cut off. Grants start read-only. The operator may add
either approval-gated power requests or narrowly curated direct operator
capabilities, which are displayed as mutation authority at consent time.

- **Effective authority is an intersection**, re-evaluated on every single tool
  call with no cache anywhere:

  ```
  active OAuth grant
    ∩ unexpired, audience-bound access token
    ∩ approved server allowlist
    ∩ approved MCP scopes
    ∩ the owner's live ServerManager RBAC
  ```

  Revocation, demotion, a membership change, or an allowlist change lands on the
  next call. A cache here would be precisely a window in which a revoked grant
  still works, so there is none.
- **The endpoint is a tool facade, never a proxy.** No tool accepts a URL, an API
  path, a shell command, SQL, a filesystem path, an environment or process
  reference, a node id, a secret name, or a raw Minecraft console command. There
  is no "call this endpoint" escape hatch, and no kill, reinstall, restore,
  settings-write, node, user, or integration surface exists as a tool at all.
  Three files are readable and no others — the current console log, the newest
  crash report, and `server.properties` — because an agent that cannot read a
  stack trace cannot diagnose a crash, and was reduced to guessing at which mod
  to disable. Those paths are constants in the facade, no tool takes a path, and
  the operator adapter re-checks the set before any handler runs, so a tool added
  later cannot widen it.
  `run_console_command` is not an exception to this and is not a console: it
  accepts a *verb* from a closed table plus arguments that the verb's own builder
  parses, and ServerManager renders the command line itself. A command string
  never crosses the boundary inbound, so `op`, `ban`, `stop`, `execute`,
  `reload`, `gamerule`, and every mod-added command are unreachable however the
  model is steered — which matters because the same facade returns untrusted log
  and mod text to that model. Arguments are length-bounded and reject control
  characters, so one call cannot deliver a second command.
- **Nothing model-facing ever sees** a node bearer token, `.connection.cfg`,
  `secrets.env`, an integration secret, a password hash, a session, a JWT, an
  access key, or another user's data.
- **Read scopes are read-only.** `mcp:servers.read`, `mcp:diagnostics.read`,
  `mcp:logs.read`, `mcp:metrics.read`, and `mcp:audit.read` each require live
  `view` on the specific server.
- **Three reads go further and are consented separately.** `mcp:logs.raw` returns
  the raw tail of the console log or the newest crash report, `mcp:config.read`
  returns `server.properties`, and `mcp:players.read` returns the whitelist, ban
  list and recently seen players. The first two require live `files.read` and the
  third live `players.inspect`, so none of them shows a delegation anything its
  owner cannot already see. They are separate scopes rather than part of
  `mcp:logs.read` because that scope consents to ServerManager's *indexed*
  warnings — a curated extract — while these return whatever the server wrote,
  which is where a pasted credential would be, and who plays there. Secret-shaped
  values are redacted on the way out, and `server.properties` is redacted by
  property name so an `rcon.password` cannot survive by being split from its key.
- **The scope vocabulary never names a capability the facade lacks.** There is no
  `mcp:files.read`: reading two fixed diagnostic files is `mcp:logs.raw`, because
  a scope name is the part a human actually reads on the consent screen and it
  must not promise file access that does not exist. The naming guard in
  `apps/api/internal/store/mcp_operator_scopes_test.go` enforces this.
- **Direct operator scopes are narrow and explicit.** `mcp:power.start`,
  `mcp:power.stop`, and `mcp:power.restart` require the matching live power leaf;
  `mcp:mods.read` requires live mod-group access; and `mcp:mods.install`,
  `mcp:mods.update`, and `mcp:mods.remove` require their matching leaves.
  These routine tools execute immediately once granted. `mcp:backups.create`
  is separate: it requires live backup-create authority and a client-mediated
  human confirmation for the exact server on every call. The confirmation is
  protocol state, not a model-authored tool argument; declining, cancelling,
  tampering with it, or using a client without elicitation support fails closed
  before the backup backend is called. The consent screen never selects mutation
  authority by default. The **Server operator** preset selects routine power and
  mod operations plus diagnostics, but deliberately does not select
  `mcp:backups.create`, `mcp:actions.request`, `mcp:players.whitelist`, or
  `mcp:console.run`.
- **Player and console scopes are bounded and separately ticked.**
  `mcp:players.whitelist` requires the live `players.whitelist` leaf and can only
  add or remove one validated player name — it cannot op, ban, or kick, and it
  does not toggle whitelist enforcement. `mcp:console.run` requires live
  `console` access as a floor, and each verb is additionally checked against the
  permission its effect needs (`kick` needs `players.kick`, `whitelist list`
  and `whitelist reload` need `players.whitelist`), so holding the console scope never widens what its
  holder could already do. Neither is in a preset: both are ticked individually,
  like `mcp:backups.create`.
- **Operator tools are a closed vocabulary.** Direct execution is limited to
  graceful start/stop/restart, installed-mod inspection, compatible-mod search,
  update discovery, mod install/update/enable/disable/remove, backup
  create/status listing, whitelist add/remove for one validated player, and the
  console verbs `list`, `say`, `save-all`, `kick`, `time`, `weather`,
  `difficulty`, and `whitelist list|reload`. Turning whitelist enforcement on
  or off is deliberately not a console verb: switching it off opens the server
  to anyone who can reach it, so it stays a dashboard decision. Every console verb is
  reversible, confers no authority, affects no availability, and takes no nested
  command; adding one is a deliberate security decision recorded in
  `apps/api/internal/mcpserver/console.go`. Mod sources and identifiers are validated; searches
  derive loader and Minecraft version from the selected server. Read-only mod
  listing performs no reconciliation writes and omits hashes/install paths.
  Backup polling exposes status fields rather than raw backend metadata, and
  operator failures are application-authored rather than relayed transport
  errors. Dependency-breaking disable/remove operations require an explicit
  acknowledgement. Backup consent is never inferred from a diagnosis, repair,
  restart, or mod operation; the MCP instructions require the agent to ask and
  wait when it merely recommends a backup, and the supplied authorization is
  recorded in the audit trail.
  Backup restore/delete, kill, reinstall, migration, cloning, raw console, files,
  settings, tasks, nodes, users, integrations, arbitrary URLs and arbitrary API
  paths remain unreachable.
- **Actions are requests, not actions.** `mcp:actions.request` lets an agent file
  a start/stop/restart request with a stated reason. It never touches the node.
  The owner approves it in the dashboard with the same password + TOTP step-up
  used for key issuance, and execution is at most once by construction: a
  conditional `UPDATE` claims the request, and only the caller that changes a row
  proceeds. Approval then **re-checks** the grant and the owner's live permission
  for that exact action before calling the node. A repeated approval, a
  concurrent approval, and agent polling all observe state; none repeats the
  action. A request expires 15 minutes after it is filed.
- **The approval step is configurable, and relaxing it needs a step-up.** The
  owner can set an account-level default and a per-connection override for three
  things: whether approving a request needs the password step-up, whether
  lifecycle actions are approved automatically, and whether version upgrades
  are. Nothing is configured by default and every unset value — including on a
  read error — resolves to the secure state: step-up required, nothing
  automatic. Upgrades have their own flag rather than riding on the lifecycle
  one, because an upgrade reinstalls the runtime and can roll a world back to a
  restore point. Changing a setting in the direction that *weakens* the gate
  requires a fresh password + TOTP step-up of its own, so a live session cannot
  quietly switch the protection off and then approve whatever it likes;
  tightening it back up is deliberately free.

  Automatic approval removes the human from the decision, not from the record.
  An auto-approved action still goes through the same claim, the same post-claim
  re-check of the grant and live permission, and the same at-most-once
  execution; it is audited under `mcp.action.auto_approved` rather than
  `mcp.action.approved` with a `NULL` decider, so no reader of the log is ever
  told a human decided something no human saw; and the owner still gets a
  notification saying it ran unattended. The agent-facing instructions say this
  too, so a model is never told a guarantee the owner has switched off.
- **Returned server content is untrusted evidence.** A hostile log line, mod
  name, or MOTD reaches the model's context, so every response separates trusted
  panel metadata from server-authored text, marks the latter `untrusted: true`,
  bounds each string and array with an explicit truncation count, and runs a
  redaction pass that strips token-, key-, password-, and connection-string-shaped
  text before it leaves the process. The server's static instructions say plainly
  that this content must not be treated as instructions or used to justify wider
  access.
- **Tokens are audience-bound.** An access token is valid only for the exact
  canonical resource URL it was issued for, so a token leaked from one deployment
  cannot be replayed against another. Lifetimes: authorization code 60 s, access
  token 15 min, refresh token 30 d, grant 30 d by default and 90 d maximum. A
  refresh token never outlives its grant.
- **Replay is self-revoking.** Consuming an authorization code twice revokes the
  grant. Presenting an already-used refresh token more than 30 seconds after it
  was rotated revokes the grant and every token under it. Inside those 30
  seconds a second presentation is a retry after a lost response or a second
  session sharing the stored credential, not a leak: it gets a sibling pair and
  nothing is revoked. Rotation consumes the old token and issues the new pair
  in one transaction, so a failed rotation leaves the old token usable.
- **Revocation is bound to the client.** `POST /api/v1/oauth/revoke` requires
  `client_id` and only revokes a token issued to that client — the same binding
  refresh rotation enforces — so observing a refresh token is not enough to
  retire someone else's delegation.
- **Public clients only, PKCE mandatory.** The target clients run on the
  operator's own machine and cannot keep a secret, so no client secret is ever
  issued or accepted, and `S256` is the only challenge method advertised or
  allowed. Redirect URIs are matched **exactly**; an unknown client or an
  unmatched redirect URI renders an error page and never redirects, which is the
  open-redirect boundary.
- **Consent is human-only and fails closed.** The consent screen is an
  authenticated dashboard route, so an unauthenticated visitor is sent to login
  and returned afterwards. Machine principals — access keys and MCP tokens alike
  — are refused on the consent, grant-management, and approval routes, so a
  credential can never approve its own successor or widen itself. A user can only
  delegate scopes they currently hold on the servers they select, re-checked at
  approval.
- **Credential families cannot be confused.** MCP secrets carry their own
  reserved prefixes (`mcsm_mcpc_`, `mcsm_mcpa_`, `mcsm_mcpr_`), distinct from
  `mcsm_pat_`, and are stored SHA-256-hashed like access keys. An agent access
  key presented to the MCP endpoint is **always** rejected; there is no setting
  that admits one. `MCP_EXPLAIN_ACCESS_KEY_REJECTION=true` only decides whether
  the 401 says *why*, which is a diagnostic and not a capability.
- **Attribution.** Audit rows for a delegated action name the human owner, the
  grant, and the client, for request, approval, denial, execution, and failure.
  Revoking a grant does not erase its history.
- **Off unless configured.** The whole surface stays unmounted until a public
  origin resolves (`MCP_PUBLIC_ORIGIN`, else `APP_ORIGIN`, else a loopback
  request). A deployment that cannot state its own external address cannot mint
  an honestly-bound token, so it 404s rather than half-working. `http` is
  accepted only for loopback. The loopback fallback requires *both* that the
  request arrived from a local address and that it named a local host, and a
  request carrying any forwarding header (`Forwarded`, `X-Forwarded-*`,
  `X-Real-IP`) is never treated as local. So an upstream proxy that rewrites
  `Host` to `127.0.0.1`, or passes the client's `Host` through without
  forwarding its address, cannot hand a public deployment a cleartext audience; a deployment that trips this fails closed and
  logs the variable to set.
- **Rate limiting** is separate from the authenticated limiter: the OAuth
  endpoints are bucketed per IP (they are unauthenticated by definition) and the
  MCP resource per grant.
- **Self-registered clients must call back to the operator's own machine.**
  Dynamic registration (RFC 7591) is open by necessity, so its redirect URIs are
  restricted to loopback. Without that restriction anyone could register a client
  whose callback is a host they control, send the resulting `/authorize` link to
  an operator, and receive the code the moment it was approved — the consent
  screen's warnings are a request that the human notice, which is weaker than
  refusing the address. Every supported client (Claude Code, Codex, Hermes) uses
  a loopback callback, so this costs them nothing. A hosted client needs an
  administrator-registered entry, or `MCP_ALLOW_REMOTE_REDIRECTS=true` to accept
  the trade deliberately.
- **Backup confirmations are single-use.** `create_server_backup` asks a human
  through the MCP elicitation round trip, and the state tying the answer to the
  question is minted at random, stored hashed, bound to one grant and one server,
  redeemed at most once, and expires in ten minutes. One answer authorizes one
  backup.
- **Retention.** The registration and authorization endpoints are unauthenticated
  and write durable rows, so a background sweep removes what can no longer
  authorize anything: lapsed authorization requests and their codes, expired or
  revoked tokens, settled approval requests, spent confirmations, and registered
  clients with no grant attached. Nothing load-bearing depends on it — every
  check re-reads expiry live — and a client that still holds a grant is never
  removed.

### What this does not defend against

- A compromised client machine. The token lives there; an attacker with it has
  the grant's authority until it is revoked or expires.
- An operator who approves a request they did not initiate. The consent screen
  names the client's self-chosen name and its redirect host precisely so that
  judgement is possible, but it cannot be made for them. The loopback
  restriction on dynamic registration narrows this considerably — a
  self-registered client can only deliver the code to the operator's own machine
  — but it does not close it for pre-registered or opted-in remote clients.
- A model persuaded by hostile server content to take an in-scope action. With
  only `mcp:actions.request`, the password/TOTP approval step is the control. A
  direct operator grant intentionally removes that second prompt, so its controls
  are the closed tool schema, exact scopes, server allowlist, live owner RBAC,
  short expiry, audit trail, and immediate revocation. Grant it only while the
  agent and its machine are actively supervised.

Setup, connection, and revocation guidance is in [operations.md](operations.md);
the reverse-proxy and TLS requirements are in [deployment.md](deployment.md).

## Server isolation

Everything a Minecraft server loads — every mod and plugin — runs in its JVM.
When servers ran as the same user as the agent and the API, that code could
read the panel's database and its encryption key, the API's environment, and
every other server's helper-mod launch token, which made `files.write`,
`mods.install` and installing any compromised mod equivalent to panel admin.

On a provisioned host, servers (and the installers that build them, which run
downloaded code too) run as a dedicated user, `mcsm-mc`:

- The agent starts them through `mcsm-launch`, a small helper installed
  root-owned (`root:mcsm 0750`) with only `cap_setuid,cap_setgid`. It can
  switch to the one user named in `/etc/mcsm/server-user` (default `mcsm-mc`)
  and refuses root. It sets `no_new_privs`, clears its ambient capabilities and
  execs the JVM in place, so the server has no capabilities and can never gain
  any. The helper is built into the root-only provisioning bundle, never shipped
  with the agent binary, because it carries capabilities.
- A server can read and write its own directory (the agent shares it through
  the `mcsm-mc` group before each start) and nothing of the panel's: the API's
  data directory is `0700`, `.mcsm-run` (console logs, launch tokens) and
  `mcsm-backups` are the agent's alone, and the agent and API processes are
  non-dumpable, so their `/proc` entries are closed to it.
- The agent keeps `CAP_KILL` to stop a server running as another user, and
  clears it from its ambient set so no child inherits it. It recognises an
  isolated server at reattach by its start time, since that server's working
  directory is hidden from it.
- Servers receive an allowlisted environment (`PATH`, `HOME`, locale, Java
  settings), never the agent's, which holds the agent token.

`AGENT_ISOLATE_SERVERS` chooses the mode: unset isolates when the host is set
up for it; `1` (what provisioning sets) requires it and refuses to start servers
otherwise; **`0` is the opt-out**, set in `secrets.env`: servers run as `mcsm`
again, with everything `mcsm` can reach — for example so a mod can act as the
panel's user when nothing else on the host can. With the opt-out, `files.write`
and `mods.install` are again equivalent to panel admin. Each node reports its
mode, and why servers are not isolated when they are not, on the Nodes page.

What isolation does not do: all servers share `mcsm-mc`, so one server's code
can reach another server's files; and the API and the agent still share `mcsm`.

The API's own unit is hardened as well (`deploy/systemd/mcsm-api.service.d/`):
no new privileges, private `/tmp`, read-only system directories, hidden home
directories and other users' processes.

## Helper-mod index

The agent installs the helper mod from a version index it downloads. The index
is signed (ed25519, `versions.json.sig`) and the agent verifies it — fetched and
cached copies alike — against `MCSM_HELPER_INDEX_PUBKEY` before trusting any
entry; an unsigned or tampered index is refused. `MCSM_HELPER_INDEX_INSECURE=1`
turns the check off for development only. Signing is described in
`apps/mod/INDEX-FORMAT.md`.

## Audit trail

Every human change is recorded with who made it (and, for automation, which
access key or MCP grant), from where, and what changed: power actions, console
commands (including those typed into the console socket, which the API reads
before forwarding and refuses when they hold a line break), player moderation
and player-data deletion, file writes, deletes, renames, uploads and downloads,
users, nodes, folders, tasks and their runs, backups and restores, settings,
sign-ins, failed sign-ins (with a reason and a short hash of the address, never
the address), and logouts. Proxied changes record the node's answer, so a
refusal reads as a refusal. Background work is attributed to the schedule, run,
person or delegation that started it.

Audit rows outlive the server they describe (they keep its id and a snapshot of
its name), and are kept for `AUDIT_RETENTION_DAYS` (default 365). The audit log
page (`GET /api/v1/audit-log`) filters by server, user, action and time;
non-admins see rows about their servers and their own actions only.

## Host reboot

Rebooting a node's host is the most disruptive action the panel has, so it is
gated at three layers:

- **API:** global admins only, on an interactive sign-in. Access keys and MCP
  grants are refused by the router (`requireAdmin`, `requireHuman`), whoever
  owns them. The request needs the current password and, when enrolled, a TOTP
  code, spending from the same throttle as login. Every attempt is audited:
  `node.reboot`, `node.reboot.denied`, or `node.reboot.failed`.
- **Agent:** refuses unless `AGENT_ALLOW_REBOOT=1`. Before stopping any server
  it asks logind whether it may reboot (`CanReboot`), so a refused reboot never
  leaves the host up with its servers down. A second request while one is in
  progress is refused.
- **Deploy path:** uploads land in a root-only staging directory
  (`/root/mcsm-deploy`), never in the `mcsm`-owned data directory, and the
  applier refuses to run from anywhere `mcsm` can write. Otherwise anything
  running as `mcsm` — the API, the agent, a Minecraft server or its mods —
  could swap the root-run applier or provisioning bundle mid-deploy.
- **OS:** the agent runs unprivileged and reboots through logind, so a polkit
  rule must grant its user `org.freedesktop.login1.reboot` (and
  `reboot-multiple-sessions`) — nothing else, not power-off and not inhibitor
  overrides. No sudo and no shell are involved; both commands have fixed
  arguments. The grant lives in `deploy/polkit/` and is installed by host
  provisioning.

## Multi-factor auth (optional TOTP)

- Users can enable time-based one-time-password (TOTP) MFA from Settings →
  Security. Enrollment is verify-before-enable; enabling returns 10 single-use
  recovery codes (shown once, stored only as SHA-256 hashes).
- The TOTP secret is encrypted at rest with the app encryption key.
- At login, an MFA-enabled account must supply a current code (or a recovery
  code); the server returns `mfa_required` after a correct password to drive the
  two-step prompt. Failed codes count against the login throttle; the expected
  "need a code" step does not.
- A code is accepted once (RFC 6238 §5.2): the time step of the last accepted
  code is kept per user, and that step or an earlier one is refused at login,
  enrollment and step-up, so a code read off a screen can't be reused while
  it is still current.
- Starting enrollment, disabling MFA, and generating new recovery codes each
  need a step-up: the current password plus a current code (or a recovery code).
  Wrong attempts spend from the login throttle. An admin can clear a locked-out
  user's MFA via `PUT /users/{id}` with `disable_mfa: true` (lost-authenticator
  recovery).

## Sessions

- Each login is a refresh-token session recording its device (user agent) and
  IP. Refreshing rotates the token in place, so a session keeps its identity and
  original login time across the 15-minute access-token cycle.
- Users review and revoke their sessions from Settings → Security
  (`GET/DELETE /auth/sessions`, `POST /auth/sessions/revoke-others`). Deleting a
  user cascade-revokes their sessions; resetting a password or demoting a user
  ends their sessions, and changing your own password (`POST /auth/password`,
  which needs the current one) ends your other sessions.
- A sign-in from a browser or system the account has not used before raises an
  alert to the account's owner (`auth.new_device`).

## Brute-force, enumeration, and DoS controls

- Failed logins lock the source IP aggressively (exponential backoff to 15 min)
  and the targeted account leniently (short cap), so a single attacker is stopped
  without letting anyone deny a real user access to their account for long.
- A failed login spends the same CPU whether or not the account exists, so
  response timing can't enumerate valid accounts.
- Authenticated traffic is rate-limited per caller (per user id, else IP);
  public routes (public status pages, token refresh, logout) have their own
  per-IP limit.
- `ReadHeaderTimeout` bounds slow-header (slowloris) connections.
- Passwords set through the API must be at least 10 characters.
- Non-multipart request bodies are size-capped (API 8 MiB, agent 32 MiB);
  uploads stream through the multipart path.
- Internal errors are logged server-side and returned to clients as a generic
  message, so SQL/path/agent details don't leak. Every error answers in one
  envelope, `{error, code, retryable, field?, details?}`, and a database
  failure is never reported as a missing row.

## Scheduled-task authorization

- A scheduled task can only do what its creator could do by hand: creating a
  `command`/`restart`/`stop`/`backup`/`mod_update` task requires the matching
  per-server permission, not just the broad `tasks` permission.
- Tasks are attributed to their creator and **re-authorized at fire time** — a
  task whose creator was deleted or lost the relevant permission is skipped, so a
  task can't become a standing backdoor.

## Admin role freshness

- Admin-only routes read the role fresh from the database on every request, so a
  demotion or deletion takes effect immediately rather than lingering for the
  life of an already-issued access token.

## Network exposure

- Only the reverse proxy faces the internet; the API (`:8081`) and agent
  (`:8090`) bind loopback. The agent refuses a non-loopback bind without TLS
  unless `AGENT_ALLOW_INSECURE=1`.
- `APP_ORIGIN` pins the WebSocket Origin allowlist.
- `TRUSTED_PROXIES` controls which peers may set `X-Forwarded-*`; from anyone
  else those headers are stripped (default: loopback only). The client address
  is `X-Real-IP` from a trusted proxy, else the rightmost `X-Forwarded-For` hop
  that is not a trusted proxy, so a client cannot choose the address its login
  throttle and audit rows are keyed on.
- `/api/v1/health` answers only `{"status":"ok"}` to anything but a direct
  loopback caller, which also gets the version.
- Outbound webhooks are signed over the delivery time and the body
  (`X-MCSM-Timestamp`, `X-MCSM-Signature-256: t=…,v1=…`), so a captured
  delivery cannot be replayed later.
- Webhooks and mod downloads only connect to public addresses, checked on the
  resolved address at dial time (so DNS rebinding doesn't help). Loopback,
  private, link-local, CGNAT and reserved ranges are refused, and NAT64 and
  6to4 addresses are judged by the IPv4 address they carry.

## Production Startup Guards

Outside `MCSM_DEV_MODE=1` or a development `APP_ENV`, the API requires a
configured `JWT_SECRET`, and the API and agent reject the default
`dev-agent-token`.

## Future Work

- Per-process state (the shared password throttle, download tickets) would
  move to a shared store for a multi-node API deployment.
- An isolated automation/MCP gateway, if agents stop being fully
  operator-controlled, if destructive actions need human approval, or if a
  model-facing tool schema has to be centrally curated. Access keys authorize a
  trusted client; they do not sandbox it or judge the intent of the commands it
  sends.
