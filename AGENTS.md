# ServerManager agent guide

## Scope

This repository contains the ServerManager web app, Go API and agent, and the
Minecraft helper mod. Follow a more specific `AGENTS.md` when working below a
directory that contains one.

## Working agreements

- Understand the affected flow before editing and fix root causes rather than
  masking symptoms.
- Preserve unrelated and user-owned working-tree changes.
- Never commit credentials, recovery codes, runtime data, generated builds, or
  `.connection.cfg`.
- Add or update focused tests for behavior changes. Run the checks relevant to
  every component touched.
- Keep wire protocols and released helper-mod metadata backward compatible.
  Follow `apps/mod/AGENTS.md` for mod work.

## Repository layout

- `apps/web` — React/Vite frontend
- `apps/api` — Go API and SQLite migrations
- `apps/agent` — Go server-management agent
- `apps/mod` — Minecraft helper mod
- `docs` — architecture and deployment documentation
- `scripts` — production deployment tooling

## Common checks

```powershell
make test
cd apps/web
pnpm typecheck
pnpm lint
pnpm build
```

Run component-specific Go tests from `apps/api` or `apps/agent` while iterating.
Use the repository's existing package manager and do not hand-edit generated
artifacts.

## Production deployment

Deploy only when the user explicitly asks. Use the checked-in wrapper instead
of hand-written SSH or upload commands:

```powershell
.\scripts\Deploy-Dashboard.ps1 -Test
.\scripts\Deploy-Dashboard.ps1 -Part web
.\scripts\Deploy-Dashboard.ps1 -Part binaries
.\scripts\Deploy-Dashboard.ps1 -Rollback
```

The default deployment includes web, API, and agent. `-Test` is expected for
non-trivial changes. A failed build must not be deployed.

The wrapper protects machine-local connection details and performs upload,
service restart, rollback preparation, and basic health checks. Do not print or
inspect its encrypted `.connection.cfg`. Do not expose the production host,
credentials, domain, or values from `/srv/dashboard/data/secrets.env`.

After deployment, verify the affected public behavior in addition to the
wrapper's service and HTTP health checks. The production app is hosted below
`/dashboard/`; deployment builds set the required Vite base path.
