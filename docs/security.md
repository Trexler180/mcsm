# Security

## Auth Model

- **Global admins** manage nodes, users, app settings, the global audit log, and
  every server. Creating servers and changing start-command inputs is admin-only
  (see below).
- **Server owners and collaborators** act on individual servers through a
  granular per-server permission system (`server_members` /
  `server_permissions`): view, console, power (start/stop/restart/kill),
  settings, files (read/write/delete), mods (install/update/remove), backups
  (create/restore/delete), players (whitelist/kick/ban/op), and tasks. Each route
  is gated on the specific permission, and the global admin role is re-read from
  the database on every check so a demotion takes effect immediately.

The granular permission model is live (not reserved). The `api_keys` table is
still reserved for future automation use.

### Privileged start-command fields

`java_binary`, `jvm_args`, and `directory_path` are inputs the agent executes,
so changing them is effectively host code execution. They are editable by global
admins only — a server-scoped `settings` collaborator cannot change them. As
defense in depth, the agent independently refuses to launch anything whose
basename is not a Java executable (`java`/`javaw`).

## Tokens

- JWT access tokens (15 min) authenticate ordinary API requests.
- Refresh tokens are stored only as hashed rows and rotated on use. Browser
  refresh tokens are `HttpOnly`, `SameSite=Strict` cookies scoped to
  `/api/v1/auth`; the frontend stores only the short-lived access token.
- Query-string tokens are never accepted. WebSocket/download endpoints that
  cannot carry an Authorization header accept a short-lived, single-use ticket
  minted from an authenticated request instead.
- Agent calls use bearer tokens stored **encrypted at rest** in the API database
  (AES-256-GCM under the app encryption key) and never emitted in node JSON. The
  agent compares the presented token in constant time.

## Multi-factor auth (optional TOTP)

- Users can enable time-based one-time-password (TOTP) MFA from Settings →
  Security. Enrollment is verify-before-enable; enabling returns 10 single-use
  recovery codes (shown once, stored only as SHA-256 hashes).
- The TOTP secret is encrypted at rest with the app encryption key.
- At login, an MFA-enabled account must supply a current code (or a recovery
  code); the server returns `mfa_required` after a correct password to drive the
  two-step prompt. Failed codes count against the login throttle; the expected
  "need a code" step does not.
- Disabling MFA requires a current code. An admin can clear a locked-out user's
  MFA via `PUT /users/{id}` with `disable_mfa: true` (lost-authenticator
  recovery).

## Sessions

- Each login is a refresh-token session recording its device (user agent) and
  IP. Refreshing rotates the token in place, so a session keeps its identity and
  original login time across the 15-minute access-token cycle.
- Users review and revoke their sessions from Settings → Security
  (`GET/DELETE /auth/sessions`, `POST /auth/sessions/revoke-others`). Deleting a
  user cascade-revokes their sessions.

## Brute-force, enumeration, and DoS controls

- Failed logins lock the source IP aggressively (exponential backoff to 15 min)
  and the targeted account leniently (short cap), so a single attacker is stopped
  without letting anyone deny a real user access to their account for long.
- A failed login spends the same CPU whether or not the account exists, so
  response timing can't enumerate valid accounts.
- Authenticated traffic is rate-limited per caller (per user id, else IP).
- `ReadHeaderTimeout` bounds slow-header (slowloris) connections.
- Passwords set through the API must be at least 10 characters.
- Non-multipart request bodies are size-capped (API 8 MiB, agent 32 MiB);
  uploads stream through the multipart path.
- Internal errors are logged server-side and returned to clients as a generic
  message, so SQL/path/agent details don't leak.

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
  else those headers are stripped (default: loopback only).

## Production Startup Guards

Outside `MCSM_DEV_MODE=1` or a development `APP_ENV`, the API requires a
configured `JWT_SECRET`, and the API and agent reject the default
`dev-agent-token`.

## Future Work

- API keys for automation, when there is a concrete need.
- Per-process state (login throttle, download tickets) would move to a shared
  store for a multi-node API deployment.
