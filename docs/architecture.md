# Architecture

ServerManager uses a panel/agent split.

- The API is the control plane. It owns users, auth, audit log, SQLite data,
  scheduling, node metadata, and server metadata.
- The agent is the host worker. It owns local filesystem access, Java process
  management, console streaming, metrics, backups, and runtime installation.
- The web app talks only to the API.

Servers reference a node by `node_id`. When the API performs a server operation,
it loads the server, loads the node, creates an authenticated agent client, and
calls the relevant `/agent/v1/...` endpoint.

Node liveness is heartbeat based. The background poller calls each agent's
`/agent/v1/info` endpoint and persists `last_seen`, memory, disk, and CPU data.
The nodes API derives `online` from recent `last_seen` rather than performing
fresh health checks during list requests.

Current deliberate boundaries:

- SQLite only for this phase.
- Owner/admin access model only.
- Direct API-to-agent HTTP only.
- Agent tokens are stored in the database and hidden from JSON responses.
