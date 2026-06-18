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
downgrade — from **Mods → Installed → Change version**.

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
2. stop the server and take a full backup (the run aborts if the backup fails),
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

## Failure Behavior

If the API cannot reach an agent, it stops refreshing that node's `last_seen`.
Servers that were `online` or `starting` are moved to `offline` by the poller
when status calls fail.

## Postgres Path

Do not introduce a store abstraction until Postgres is a real near-term target.
The current priority is SQLite reliability, migrations, backups, and tests.
