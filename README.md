# ServerManager

ServerManager is a self-hosted Minecraft server panel with a Go API control
plane, a per-host Go agent, and a React web UI.

The supported production shape for this phase is a single host running the API,
web UI, and one local agent. SQLite is the supported database. Remote nodes,
Postgres, organizations, and granular collaborator permissions are reserved for
later architecture work.

## Local Development

On Windows, run:

```powershell
.\run.ps1
```

Defaults:

- Web: `http://localhost:3000`
- API: `http://localhost:8080`
- Agent: `http://localhost:8090`
- Admin: `admin@example.com / changeme`

The script starts all three services, sets `MCSM_DEV_MODE=1`, and auto-registers
the local agent.

## Production (native, single host)

No containers or VMs required. Build the binaries and the web bundle, then run
the three processes directly (e.g. as systemd services on Linux, or scheduled
tasks / a service wrapper on Windows). See `docs/deployment.md`.

```bash
# build
cd apps/api   && go build -o mcsm-api   ./cmd/server
cd apps/agent && go build -o mcsm-agent ./cmd/agent
cd apps/web   && pnpm install --frozen-lockfile && pnpm build   # → apps/web/dist
```

Serve `apps/web/dist` as static files behind any reverse proxy you already run,
proxying `/api` to the API on `:8080`. Persistent state:

- SQLite DB at `DATABASE_PATH`
- server directories and local backup archives under `SERVER_ROOT`

## Required Production Secrets

Outside explicit development mode, the API refuses to start without
`JWT_SECRET`, and the agent/API refuse the default `dev-agent-token`.

Use long random values for:

- `JWT_SECRET`
- `AGENT_TOKEN`
- `ADMIN_PASSWORD`

## Backup And Restore

Back up the SQLite database (`DATABASE_PATH`) and the `SERVER_ROOT` directory
together, after stopping the services or ensuring SQLite has checkpointed
cleanly. A plain `tar`/`zip` of both is enough:

```bash
tar czf mcsm-backup.tgz "$DATABASE_PATH" "$SERVER_ROOT"
```

Restore by stopping the services, extracting the archive back to the same
paths, and starting the services again.

## Checks

```bash
cd apps/api && go test ./...
cd apps/agent && go test ./...
cd apps/web && pnpm lint && pnpm build
```

See `docs/` for architecture, deployment, security, and operations notes.
