# ServerManager — Handoff Plan for Codex

Living doc. Update as work lands. Sections in order Codex should read.

---

## 1. What this is

Self-hosted Minecraft Server Manager ("mcsm"). Pterodactyl-style panel + agent split.

- `apps/api` — control plane. Go 1.25, chi router, JWT auth, SQLite (modernc, WAL), goose migrations, robfig/cron, coder/websocket. Talks to agents over HTTP+WS.
- `apps/agent` — per-host process manager. Go 1.25, chi router, bearer token auth. Spawns `java`, captures stdout/stderr, exposes console+metrics WS, file CRUD, ZIP backups, auto-installs server runtimes (Paper/Purpur/Vanilla/Fabric/Quilt/Spigot/Forge/NeoForge).
- `apps/web` — React 18 + Vite + Tailwind + Tanstack Router/Query + Radix + xterm + CodeMirror + recharts. Zustand for auth/notif state.
- `servers/` — per-server working dirs created by agent. `servers/mcsm-backups/<server_id>/<backup_id>.zip` for backups.

Dev loop: `./run.ps1` (Win) or `make dev-api / dev-agent / dev-web` (3 terminals). API auto-registers a local agent node when `AUTO_REGISTER_LOCAL_AGENT=1`.

## 2. Repo state (verify before editing)

- **Git repo with history.** Default working branch is `codex/future-proof-single-host`; `master` tracks the same tip. Commit per change.
- README + docs (`docs/{architecture,deployment,security,operations}.md`) exist. This file is the task ledger; docs are user-facing truth.
- Tests exist across both Go services (auth, middleware, db, migrations, health, server-access, install). Still patchy — add tests as you touch code. `go test ./...` in `apps/api` and `apps/agent` is green.
- `apps/api/internal/store/{gen,queries}` are empty placeholder dirs — store is hand-written in `db.go`. Don't bother with sqlc unless you migrate the whole store.
- DB files `apps/api/mcsm.db*` are gitignored.
- `apps/web/node_modules` present — `pnpm install` already run. `pnpm build` is green; **`pnpm lint` is just `tsc -b` (typecheck), not real eslint** (see §4).
- CI: `.github/workflows/ci.yml` runs build + test + web build on PR.
- Docker: Dockerfiles for api/agent/web + `docker-compose.yml` + `apps/web/nginx.conf`.

## 3. Architecture map (where things live)

### API (`apps/api`)
- `cmd/server/main.go` — boot, migrations, admin seed, AUTO_REGISTER_LOCAL_AGENT, starts scheduler + poller.
- `internal/api/router.go` — single source of truth for routes; check before adding endpoints.
- `internal/api/server_access.go` — RBAC middleware; admin bypass + `UserCanAccessServer` lookup against `server_permissions`.
- `internal/api/handlers/*.go` — one file per resource. Pattern: `New<X>Handlers(store) → methods take (w, r)`. Use `decode`, `writeJSON`, `writeError` from `helpers.go`.
- `internal/api/ws/hub.go` — browser↔agent WS proxy (console + metrics).
- `internal/agent/client.go` — typed client for agent HTTP+WS.
- `internal/auth/` — JWT issue/verify, bcrypt password, middleware + AdminOnly.
- `internal/store/db.go` — all SQL. Models + CRUD. Uses `uuid.New()` for IDs.
- `internal/scheduler/scheduler.go` — refreshes registry from DB every 30s. Actions: `command`, `restart`, `stop`, `backup`.
- `internal/poller/poller.go` — 15s status sweep over all servers via agent.
- `internal/mods/modrinth/client.go` — Modrinth search/version/install support.
- `migrations/001_initial.sql` — full schema. Tables: users, refresh_tokens, api_keys, nodes, servers, server_permissions, installed_mods, backup_targets, backups, scheduled_tasks, audit_log.

### Agent (`apps/agent`)
- `cmd/agent/main.go` — token-gated HTTP server, optional TLS via `AGENT_TLS_CERT/KEY`.
- `internal/api/router.go` — `/agent/v1` routes; **single** auth middleware (bearer token).
- `internal/api/handlers/*.go` — health, info, java detect, metrics, servers, console, files, backup, players, paths.
- `internal/process/{manager,instance}.go` — spawns `java`, parses join/leave lines, broadcasts console events to fan-out subscribers, 500-line history ring buffer.
- `internal/install/install.go` — platform-specific server JAR install. Forge/NeoForge write `mcsm-runtime.txt` which `instance.start()` reads instead of `-jar server.jar`.
- `internal/files/fs.go` — sandboxed file ops (assume — verify path validation).
- `internal/metrics/metrics.go` — gopsutil-style host + process metrics.
- `internal/java/detect.go` + `paths_{linux,windows}.go` — auto-discover JDK installs.

### Web (`apps/web/src`)
- `routes/` — Tanstack file-based routing. `__root.tsx` redirects unauth → `/login`.
- `lib/api.ts` — fetch wrapper, types in `lib/types.ts`.
- `lib/ws.ts` — WS helpers for console + metrics.
- `components/console/terminal.tsx` — xterm.
- `components/files/{browser,editor}.tsx` — CodeMirror editor.
- `components/mods/search.tsx` — Modrinth UI.
- `components/players/panel.tsx` — bans/ops/whitelist.
- `store/{auth,notifications}.ts` — Zustand.

## 4. Gaps & next work (priority order)

Many original P0/P1 items have landed — see `## 8 Changelog`. Open items below.

### P0 — correctness / safety
1. **`apps/web/src/routes/servers/$id.tsx` is 2819 lines** (was flagged at 1000+, now nearly tripled). Top maintainability hotspot. Split into per-tab components under `components/servers/` (console, files, mods, backups, tasks, players, settings, versions). Route file should be thin.
2. **Web ships one 1.4 MB JS chunk** (412 KB gzip), no code-splitting. Lazy-load heavy deps per route — xterm, codemirror, recharts — via dynamic `import()` / `manualChunks`. Cuts initial load a lot.
3. **`pnpm lint` is a lie.** Script is `tsc -b --pretty false` (typecheck only); `eslint` is a devDependency with **no `eslint.config.js` and no script invoking it**. Either add a flat eslint config + a real `lint` script, or drop the dead dep. README/CI both imply real linting.

### P1 — verify or finish
4. **`api_keys` table.** Docs (`docs/security.md`) declare it reserved/future, not a bug. If staying deferred, leave it; only build the handler + `Authorization: ApiKey` path when there's a concrete automation need.
5. **CreateBackup double-registers dir** (`apps/api/internal/api/handlers/backups.go`). Verify the pre-go RegisterDir + in-goroutine re-RegisterDirs duplication was removed; if not, dedupe so failures short-circuit cleanly.
6. **Mods install loader logic** (`mods.go`). Verify loader is set whenever platform is fabric/quilt/forge/neoforge/spigot/paper, not only when `loader_version != nil`. Check against Modrinth taxonomy.
7. **Refresh-token rotation.** `docs/security.md` claims refresh tokens rotate on use. Confirm `handlers/auth.go` actually rotates + invalidates the old token (no replay window). Add a regression test if missing.
8. **Backup targets ≠ local.** Schema lists `type` + `config` (S3/B2/Restic implied); only local ZIP exists. Either narrow the schema OR add S3 (AWS SDK v2, stream upload from agent).
9. **`display_name` field, no PATCH endpoint.** Add `PUT /api/v1/auth/me` (self) + `PUT /api/v1/users/{id}` (admin).
10. **Server permissions UI.** `server_permissions.permissions` JSON shape still undefined and `requireServerAccess` is boolean. Docs defer this deliberately — only build if granular collab moves in scope. Define enum (`view`, `console`, `files`, `start_stop`, `mods`, `backups`, `tasks`, `admin`) when you do.

### P2 — quality / DX
11. **SecurityHeaders middleware is minimal** (`internal/api/middleware/security.go`): nosniff + frame-deny + referrer-policy only. Add CSP (web is static-served via nginx → put CSP in `apps/web/nginx.conf`), and HSTS when TLS-terminated.
12. **No metrics export.** Add `/metrics` Prometheus endpoint (request counts, scheduler runs, backup outcomes).
13. **No integration test for API↔agent.** Spin agent on random port in `TestMain`, drive a couple round-trips.
14. **TLS for agent is half-done** — flags exist but thin docs. Document cert generation + add an `INSECURE_HTTP=1` guard so prod accidents are loud.
15. **Console WS history backlog on connect** — verify subscribe() history reaches the browser through the agent→browser proxy. Add a regression test.
16. **Player kick/ban/whitelist panel** — verify `apps/agent/internal/api/handlers/players.go` covers every command the UI exposes.
17. **Encrypt stored agent tokens at rest** (already listed in `docs/security.md` future work).

### P3 — nice to have
25. Auto-update server.jar (cron-driven re-install + diff via SHA256 stored on `installed_mods`).
26. SFTP-style file upload via WebDAV or chunked PUT for files >100 MB (current upload is single-shot multipart).
27. Cluster mode: multiple API replicas behind shared SQLite-on-Litestream or migrate to Postgres (the store layer is small enough).
28. Plugin marketplace (Spigot resources/Polymart) — same pattern as Modrinth client.

## 5. Conventions Codex must follow

- **Commit messages**: no AI-coauthor trailer, no "🤖 Generated by …". Subject ≤50 chars. Conventional Commits if you want, but not required.
- **Go**: errors wrap with `%w`; HTTP handlers always write through `writeJSON`/`writeError`; never return non-JSON error bodies. Use `auth.ClaimsFrom(r.Context())` for current user.
- **DB**: every new column needs a migration file `00X_<name>.sql` with `-- +goose Up` / `-- +goose Down`. Bump nothing else; goose handles ordering by filename.
- **Agent calls**: always `RegisterDir` before any `{id}` operation. Agent has no persistent dir map — it forgets across restarts.
- **Context deadlines**: every external call (`http.Client.Do`, agent client, exec.CommandContext) needs a ctx with timeout. The repo currently has at least 2 violations (see P0 #7).
- **Frontend**: components in `components/<area>`, route files thin. Use `api.ts` wrappers, never raw `fetch`. New routes need an entry in `routeTree.ts` (or regen via tanstack plugin).
- **Tests**: `apps/<svc> && go test ./...`. Web has eslint only; add vitest if you write JS tests.
- **No deps without justification.** Existing stack is curated.

## 6. Codex working loop

1. Read this file end-to-end.
2. `git init` + initial commit (P0 #1).
3. Pick the smallest P0 you can ship in one sitting. Don't batch.
4. Before editing: `make test` baseline. After: same + `make build`. Both must pass.
5. Manual smoke: `./run.ps1`; log in `admin@example.com / changeme`; create a Paper 1.21.4 server pointed at `servers/test-server`; start; tail console; stop.
6. Commit per change. Push only after the human reviews.

## 7. Open questions for the human

- Target deployment? Single-host docker-compose, or remote agents over WAN? Affects TLS urgency (P2 #22).
- Multi-user is in the schema (`role`, `server_permissions`) but UI is admin-centric. Is that intentional MVP scope, or backlog?
- Modrinth-only mods OK for v1, or is CurseForge a hard requirement?
- SQLite forever, or Postgres before multi-host? Decides whether to refactor `store/db.go` toward sqlc + a Dialect abstraction.

---

## 8. Changelog (resolved items)

Newest first. Move items here from `## 4` as they land.

- **Single-host hardening pass** (`feat: single-host hardening`): git history + README + `docs/`; restricted `?token=` auth to console/metrics/download (was every route — JWT leak); prod guards requiring `JWT_SECRET` and rejecting default `dev-agent-token`; SecurityHeaders middleware; public resource-pack download (constant-time ID + path allowlist); writable-dir preflight; Dockerfiles + docker-compose + nginx; node heartbeat polling; health endpoints; GH Actions CI; tests across auth/middleware/db/migrations/health/server-access.
- **Mod conflict detection, config editors, player detail, offline NBT viewing** (`3e5dfa8`).
- **MC/loader version dropdowns + apply-and-reinstall to switch versions** (`d5721c9`).
- **Clickable mod detail view** — rendered description, gallery, links (`7747875`).
- **`.mrpack` modpack install** (`73f09d2`).
- **CurseForge source** behind `CURSEFORGE_API_KEY` + source toggle UI (`c7bc737`) — was §4 #12.
- **slog structured logging + request IDs + agent graceful instance shutdown** (`f5b68dd`) — was §4 #16, #18.
- **Backup restore endpoint + retention enforcement + restore UI** (`49d409d`) — was §4 #9, #10.
- **`audit_log` wired + admin audit view** (`61e0fad`) — was §4 #3.
- **Mod search filters, version picker, updates + pinning UI** (`5ee5051`).

End. Update `## 4` as items land; move resolved items here so the next agent sees history.
