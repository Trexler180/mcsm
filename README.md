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

## Docker Compose

Create a `.env` file:

```env
JWT_SECRET=replace-with-a-long-random-secret
AGENT_TOKEN=replace-with-a-long-random-agent-token
ADMIN_PASSWORD=replace-with-an-initial-admin-password
ADMIN_EMAIL=admin@example.com
WEB_PORT=3000
```

Start the stack:

```bash
docker compose up -d --build
```

Open `http://localhost:3000`.

Data lives in named volumes:

- `mcsm-api-data` for SQLite
- `mcsm-servers` for server directories and local backup archives

## Required Production Secrets

Outside explicit development mode, the API refuses to start without
`JWT_SECRET`, and the agent/API refuse the default `dev-agent-token`.

Use long random values for:

- `JWT_SECRET`
- `AGENT_TOKEN`
- `ADMIN_PASSWORD`

## Backup And Restore

Back up both persistent volumes together while the stack is stopped, or after
ensuring SQLite has checkpointed cleanly:

```bash
docker compose down
docker run --rm -v servermanager_mcsm-api-data:/api -v "$PWD:/backup" alpine tar czf /backup/mcsm-api-data.tgz -C /api .
docker run --rm -v servermanager_mcsm-servers:/servers -v "$PWD:/backup" alpine tar czf /backup/mcsm-servers.tgz -C /servers .
docker compose up -d
```

Restore by stopping the stack, extracting the archives back into fresh volumes,
and starting the stack again.

## Checks

```bash
cd apps/api && go test ./...
cd apps/agent && go test ./...
cd apps/web && pnpm lint && pnpm build
```

See `docs/` for architecture, deployment, security, and operations notes.
