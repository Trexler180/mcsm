# Deployment

The supported production deployment is three native processes on a single host.
No containers or virtualization are used or required.

## Processes

- `mcsm-api`: Go API on `:8081`. Runs goose migrations on boot, seeds the admin
  user, auto-registers the local agent, starts the scheduler and poller.
- `mcsm-agent`: Go agent on `:8090`. Manages Java server processes and files.
- web: the static React bundle (`apps/web/dist`) served by any web server /
  reverse proxy already on the host, proxying `/api` to the API.

The API auto-registers the agent as `Local Agent` when
`AUTO_REGISTER_LOCAL_AGENT=1`, pointing at `LOCAL_AGENT_FQDN:LOCAL_AGENT_PORT`.

## Build

```bash
cd apps/api   && go build -o mcsm-api   ./cmd/server
cd apps/agent && go build -o mcsm-agent ./cmd/agent
cd apps/web   && pnpm install --frozen-lockfile && pnpm build
```

## Run

Run `mcsm-api` and `mcsm-agent` under whatever service manager the host uses
(systemd on Linux, a service wrapper or scheduled task on Windows). Set at least
`JWT_SECRET`, `AGENT_TOKEN`, `ADMIN_PASSWORD`, `DATABASE_PATH`, and
`SERVER_ROOT`. Point a reverse proxy at `apps/web/dist` for static files and
forward `/api` (including WebSocket upgrades for console/metrics) to `:8081`.

## State

- SQLite database at `DATABASE_PATH`.
- Minecraft server directories and local backup archives under `SERVER_ROOT`.

Back up both together so metadata and files stay in sync (see README).

## Upgrades

1. Read release and migration notes.
2. Back up the database and `SERVER_ROOT`.
3. Build the new binaries and web bundle.
4. Restart the services; goose migrations run on API boot.
5. Confirm `/api/v1/health` returns `{"status":"ok"}` and the node is online.

## TLS

Terminate TLS at the reverse proxy in front of the web bundle / API. If the
agent is reachable outside the host, set `AGENT_TLS_CERT` and `AGENT_TLS_KEY`;
plain public HTTP agents are not recommended.
