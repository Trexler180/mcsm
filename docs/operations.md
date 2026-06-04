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

## Failure Behavior

If the API cannot reach an agent, it stops refreshing that node's `last_seen`.
Servers that were `online` or `starting` are moved to `offline` by the poller
when status calls fail.

## Postgres Path

Do not introduce a store abstraction until Postgres is a real near-term target.
The current priority is SQLite reliability, migrations, backups, and tests.
