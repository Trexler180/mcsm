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

- **Not a git repo.** First step: `git init && git add -A && git commit -m "snapshot"` so Codex has rollback safety.
- No README. This file is current ground truth.
- One Go test only: `apps/agent/internal/install/install_test.go`. Coverage is near zero — add tests as you touch code.
- `apps/api/internal/store/{gen,queries}` are empty placeholder dirs — store is hand-written in `db.go` (783 lines). Don't bother with sqlc unless you migrate the whole store.
- DB file `apps/api/mcsm.db*` is checked in (delete or gitignore on first commit).
- `apps/web/node_modules` present — `pnpm install` already run.

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

### P0 — correctness / safety
1. **No git history.** `git init`, add `.gitignore` (bin/, node_modules/, mcsm.db*, apps/web/dist/, apps/api/mcsm.db*, .buildtools/), initial commit.
2. **No README / CONTRIBUTING.** Even one paragraph + `run.ps1` link unblocks anyone else.
3. **`audit_log` table unused.** Schema exists, zero writes. Wire `s.WriteAudit(ctx, userID, serverID, action, detail, ip)` into auth login/logout, server CRUD, start/stop/kill, backup, mod install/uninstall, scheduled-task CRUD, user CRUD. Add `GET /api/v1/audit` (admin) + per-server `GET /servers/{id}/audit`.
4. **`api_keys` table unused.** No handler, no middleware path. Either drop the table or add: `POST/GET/DELETE /api/v1/api-keys`, hash with bcrypt, extend `auth.Middleware` to accept `Authorization: ApiKey <token>`.
5. **CreateBackup double-registers dir** (`apps/api/internal/api/handlers/backups.go:75-86`). The pre-go RegisterDir succeeds, but the goroutine re-RegisterDirs even on success. Remove duplicate, then any failure short-circuits cleanly.
6. **Mods install hard-codes `loader` from `srv.Platform` only when `LoaderVersion != nil`** (`mods.go:131`). Reverse the logic — loader should be set whenever platform is fabric/quilt/forge/neoforge/spigot/paper, regardless of `loader_version`. Verify against Modrinth taxonomy.
7. **`http.Get(primaryFile.URL)`** in mod install (`mods.go:164`) bypasses context — no timeout, no cancellation. Use `http.NewRequestWithContext` + the existing client.
8. **Scheduled-task UI does not exist.** Routes wired in API but no React route — add `/servers/$id/tasks` panel (the in-place placeholders in `routes/servers/$id.tsx:399-433` are mock UI only; verify).

### P1 — features the schema implies but code doesn't deliver
9. **Backup restore.** Agent has download endpoint but no restore. Add `POST /agent/v1/servers/{id}/restore?backup_id=...` that stops the instance, wipes selected paths, unzips, restarts. Mirror with `POST /api/v1/servers/{id}/backups/{backupId}/restore`.
10. **Backup retention.** `backup_targets.retention` JSON stored, never applied. Background job: enforce `keep_last_n` + `max_age_days` per target. Hook into the scheduler.
11. **Backup targets ≠ local.** Schema lists `type` + `config` (S3, B2, Restic implied). Currently only local ZIP on the agent. Either narrow the schema OR add at least S3 (use AWS SDK v2; upload from agent, stream).
12. **CurseForge mods.** Only Modrinth. Add `internal/mods/curseforge/client.go` behind a `CURSEFORGE_API_KEY` env, plug into existing `/mods/search` with `source` param.
13. **Refresh tokens table written but never rotated.** Verify `apps/api/internal/api/handlers/auth.go` rotates on use; if not, fix replay window.
14. **`display_name` field exists, no PATCH endpoint.** Add `PUT /api/v1/auth/me` for self-profile, `PUT /api/v1/users/{id}` for admin.
15. **Server permissions UI.** `server_permissions.permissions` JSON array shape is undefined. Define enum (`view`, `console`, `files`, `start_stop`, `mods`, `backups`, `tasks`, `admin`) in code, enforce per-route in `requireServerAccess` (currently boolean), add panel under `/servers/$id/permissions`.

### P2 — quality / DX
16. **No structured logging.** `log.Printf` everywhere. Move to `log/slog`, add request ID middleware, drop the unused `chimw.Recoverer` panic body into the log instead of stdout.
17. **No metrics export.** Add `/metrics` Prometheus endpoint (request counts, scheduler runs, backup outcomes).
18. **Agent has no graceful shutdown of running MC instances on SIGTERM.** It stops the HTTP server but leaves Java children orphaned. On shutdown, iterate `Manager.instances` and `inst.stop(true, 30s)`.
19. **No CI.** Add a single GH Actions workflow: `go build ./...`, `go test ./...`, `pnpm build`, `pnpm lint`. Trigger on PR.
20. **No integration test for API↔agent.** Spin agent on random port in `TestMain`, drive a couple of round-trips.
21. **`apps/web/src/routes/servers/$id.tsx` is 1000+ lines.** Split into tab components.
22. **TLS for agent is half-done** — flags exist but no docs. Document cert generation + add `INSECURE_HTTP=1` guard so prod accidents are loud.
23. **Console WS does not push history backlog on connect** — verify (subscribe() copies history server-side, but agent→browser proxy may swallow it). Add a regression test.
24. **Player kick/ban/whitelist panel** — UI exists (`components/players/panel.tsx`) but verify backend endpoints (`apps/agent/internal/api/handlers/players.go`) cover all commands.

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

End. Update `## 4` as items land. Move resolved items to a `## 8 Changelog` section so the next agent sees history.
