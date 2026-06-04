# Deployment

The supported production deployment is Docker Compose on a single host.

## Services

- `api`: Go API on port 8080 inside the compose network.
- `agent`: Go agent on port 8090 inside the compose network.
- `web`: nginx serving the built React app and proxying `/api` to the API.

The API auto-registers the compose agent as `Local Agent` using the `agent`
service DNS name.

## Volumes

- `mcsm-api-data`: SQLite database files.
- `mcsm-servers`: Minecraft server directories and local backup archives.

Back up both volumes together when possible so metadata and files remain in
sync.

## Upgrades

1. Read release notes and migration notes.
2. Back up both volumes.
3. Pull/build the new images.
4. Start the stack and let goose migrations run on API boot.
5. Confirm `/api/v1/health` returns `{"status":"ok"}` and the node is online.

## TLS

For single-host local deployments, put TLS at a reverse proxy in front of the
web container. If exposing an agent outside the compose network, configure
`AGENT_TLS_CERT` and `AGENT_TLS_KEY`; direct public HTTP agents are not
recommended.
