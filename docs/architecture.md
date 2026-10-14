# Architecture

ServerManager uses a panel/agent split.

- The **API** is the control plane: users, sessions, permissions, the audit
  log, SQLite data, scheduling, node and server metadata, alerts, and the MCP
  facade for remote agents.
- The **agent** is the host worker: filesystem access, Java process management
  (as the isolated server user where the host allows it), console streaming,
  metrics, backups, runtime installation, and the server's own log files.
- The **web app** talks only to the API: REST for reads and actions, and one
  WebSocket (`/api/v1/events`) for everything live.

Servers reference a node by `node_id`. When the API acts on a server it loads
the server and its node, builds an authenticated agent client, and calls the
relevant `/agent/v1/...` endpoint.

## Services

Business logic that more than one caller needs lives in services rather than
HTTP handlers, so the dashboard, the scheduler, the safe-update and migration
engines, and the MCP facade share one implementation:

- **operations** — long-running work (start, reinstall, backup, restore,
  migrate, auto-update, fix, world upload) is an operation row with a phase,
  progress, result, and the actor behind it. At most one exclusive operation
  runs per server; anything else that would change the server is refused with
  `server_busy` naming the operation that holds it. Operations interrupted by a
  restart are closed at boot and reported.
- **lifecycle** — the one implementation of start, stop, restart, kill,
  reinstall, and delete: lock, status, audit, events.
- **backups** — create, restore (behind a safety backup of the current world),
  delete, download, and retention.
- **events** — an in-process bus with sequence numbers. The `/api/v1/events`
  socket filters it per user, resumes from `?since=<seq>`, and says
  `stream.reset` when the gap is too old to replay.

## Status

The agent is the source of truth for a server's process. It records why each
run ended (`requested`, `exited`, `crashed`, `startup_failure`, with the exit
code) and serves every server's status in one call. The poller polls nodes
concurrently, applies those transitions (a crash is recorded as a crash, a
clean `/stop` as a stop), and reports a server as `unknown` — never crashed —
while its node has not answered for longer than a restart takes. When the node
returns with the same process still running, the server's uptime continues.

Node liveness is heartbeat based: the poller persists `last_seen`, resources,
the agent's boot id and capabilities, and its isolation report; the nodes API
derives `online` from a recent `last_seen`.

## Data

- SQLite, opened with immediate-lock transactions and every time stored as UTC
  `YYYY-MM-DD HH:MM:SS` (older rows are normalized at startup).
- Secrets (node tokens, TOTP secrets, integration and webhook secrets) are
  encrypted with AES-GCM bound to their row; the key must be backed up with the
  database.
- Audit rows outlive the servers they describe; history tables are pruned on a
  retention schedule.
- Errors answer in one envelope, `{error, code, retryable, field?, details?}`.

## Boundaries

- SQLite only; one API process (throttles and download tickets are in memory).
- Direct API-to-agent HTTP; the agent's token is never distributed.
- Minecraft servers run as a dedicated user on provisioned Linux hosts (see
  [security.md](security.md#server-isolation)); otherwise, or with the opt-out,
  as the agent's user.
