# Master Plan — Server version migration (mod compatibility + change)

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done

## Goal

Let an operator pick a target Minecraft version and see, **before committing**, the
ratio of installed mods that do / don't have a compatible build for it — then apply
the change in one safe action: bump the server version, reinstall the runtime jar,
move every compatible mod to a target-compatible build, and auto-disable the mods
that have no build yet. Works for both **upgrade and downgrade**.

## Decisions (locked)

- **Rollback**: take a full backup before applying; on an unhealthy post-migration
  boot, restore the backup **and** rewrite the snapshotted DB rows. The version move
  is one atomic step, not per-mod isolation.
- **Scope (v1)**: change `mc_version` only (auto-bump fabric/quilt loader version if
  needed). Platform stays the same. Paper→Purpur / Fabric→Quilt is a later phase.
- **Pinned mods**: a version change is deliberate, so pinned mods are moved too
  (pin = routine-update cadence, not a version lock).
- **Incompatible mods**: disabled via the `.disabled` rename (reversible), never
  uninstalled.

## Reuse map (don't rebuild these)

- Compatibility primitive: `sourceClient.GetVersions(ctx, projectID, loader, mcVersion)`
  (`apps/api/internal/api/handlers/mods.go:65`). The preview is `Updates`
  (`mods.go:802`) generalized to an arbitrary target version.
- Target-version list: `mc.Client.GameVersions` (`apps/api/internal/mc/versions.go:89`).
- Jar swap + boot-health watch + run-progress doc + DB-row snapshot pattern:
  `autoupdate.Engine` — `swapFile` (`engine.go:528`), `startAndWatch` (`engine.go:592`),
  `runDetail`/`candidate` (`engine.go:98`,`:107`), `Trigger` detached goroutine (`engine.go:125`).
- Runtime jar swap for a new version: `ServerHandlers.Reinstall` (`servers.go:406`)
  → `agent.Client.Reinstall`.
- Backup/restore: `store.CreateBackup` + `agent.Client.Backup` / `agent.Client.Restore`
  (see `handlers/backups.go:39`,`:104`).
- `.disabled` convention: `disabledSuffix` + `SetEnabled` (`mods.go:1055`,`:1060`).
- Run row model + store methods to mirror: `store/autoupdate.go:84` (`ModUpdateRun`).

---

## Phase 1 — Compatibility preview (read-only, shippable alone)  ✅ DONE

- [x] Handler `ModHandlers.VersionCheck` (`GET /servers/{id}/mods/version-check?mc_version=X`):
      - soft-validates `X` against `mc.GameVersions(platform, snapshots=true)` (proceeds if
        the upstream list is unavailable rather than blocking the preview).
      - for each installed mod, calls `GetVersions(projectID, loader, X)` and buckets into
        `compatible` / `supported` (current build already lists `X`) / `incompatible` /
        `unmanaged` (custom + non-checkable sources) / `unknown` (lookup failed this run).
      - responds with `counts` + per-mod detail (`mod_id`, name, source, current version,
        target version/id, pinned, enabled).
      - parallelized with a bounded worker pool (`versionCheckConcurrency = 8`).
- [x] Route registered: `r.With(modsRead).Get("/mods/version-check", modH.VersionCheck)`.
- [x] Test: `classifyForTarget` table-driven over a fake source (`version_check_test.go`).
- [x] Web: `api.mods.versionCheck`, types (`ModCompat`/`VersionCheckResult`), and the
      `VersionCheckDialog` (target picker, upgrade/downgrade indicator, ratio bar, grouped
      per-mod list) wired to a "Change version" button on the installed tab.

Notes for later phases:
- A `unknown` bucket was added (not in the original plan) for transient upstream failures,
  so a network blip isn't misreported as "incompatible". The migration engine should treat
  `unknown` as leave-and-warn, never auto-disable.
- Loader-version bump for fabric/quilt is deferred to Phase 3 (the preview only needs the
  loader *name*, which `LoaderForPlatform` already gives).

## Phase 2 — Migration store + run model

- [ ] Migration `apps/api/internal/migrations/010_server_version_migrations.sql`:
      `server_version_migrations(id, server_id, from_mc_version, to_mc_version,
      backup_id, status, detail JSON, created_at, finished_at)` + index on `server_id`.
- [ ] `store/migration.go`: `CreateVersionMigration`, `UpdateVersionMigration`,
      `GetVersionMigration`, `ListVersionMigrations` — mirror `store/autoupdate.go:84+`.
- [ ] `migrations_test.go`: assert the new migration applies cleanly.

## Phase 3 — Migration engine (apply, atomic via backup)

- [ ] New `internal/migrate` package (or extend `autoupdate`) with an engine that owns
      a per-server in-flight guard (`ErrAlreadyRunning`, like `autoupdate.Engine.active`).
- [ ] `Trigger(ctx, serverID, targetMC)` → creates the run row, returns it `202`, runs
      the rest in a **detached** goroutine with a hard timeout (browser close must not
      abort a mid-restore run). Mirror `autoupdate.Trigger` (`engine.go:125`).
- [ ] `execute` flow, writing phase/message to the run detail throughout:
      1. **snapshot** rollback state in memory: `srv.MCVersion`/`LoaderVersion` + every
         affected `installed_mods` row (version, file_name, enabled) — the restore target.
      2. record prior power state (`isRunning`); stop the server (`stopServer`).
      3. **backup**: `CreateBackup` row + `agent.Backup`; keep `backup_id`. Abort the run
         if the backup fails (no safety net = don't proceed).
      4. **re-check** compatibility server-side (don't trust the client's preview payload).
      5. set `srv.MCVersion` (+ loader bump) and persist; `agent.Reinstall` the runtime jar.
      6. compatible mods (incl. pinned) → `swapFile` to target build, `UpdateMod`.
      7. incompatible mods → rename to `.disabled` on the agent, `SetModEnabled(false)`.
      8. start + `startAndWatch`.
      9. **healthy** → restore prior power state, status `success` (+ partial if some
         swaps failed). **unhealthy** → `agent.Restore(backup_id)`, rewrite the snapshot
         rows + `mc_version`, status `failed` with the boot reason.
- [ ] Audit actions: `server.migrate`, `server.migrate_reverted`, `server.migrate_failed`.
- [ ] Tests: fake agent + fake mod source covering healthy apply, unhealthy→restore,
      backup-failure abort, mixed compatible/incompatible/unmanaged sets.

## Phase 4 — API surface

- [ ] `POST /servers/{id}/migrate` (body `{mc_version}`) → engine `Trigger`. Gate with
      `settingsAccess` — same leaf as `Reinstall` (`router.go:115`) since it rewrites
      server config. (Implicitly needs mods + backup rights; document the assumption.)
- [ ] `GET /servers/{id}/migrations` and `GET /servers/{id}/migrations/{runId}` for
      history + live polling (mirror `ListUpdateRuns`/`GetUpdateRun`, `router.go:162-163`),
      gated `viewAccess`.
- [ ] Wire the engine into `NewRouter`/`NewModHandlers` construction like `autoupdate.Engine`.

## Phase 5 — Web UI

- [ ] `lib/api.ts`: `versionCheck(serverId, mcVersion)`, `migrate(serverId, mcVersion)`,
      `listMigrations` / `getMigration`. Types in `lib/types.ts`.
- [ ] New "Change version" view under the server's Mods area (sibling to the existing
      update/safe-update UI, `components/mods/safe-update-dialog.tsx`):
      - target-version picker (`mc.GameVersions`), upgrade/downgrade indicator.
      - **ratio summary** (compatible / total) + per-bucket lists, with the unmanaged
        bucket clearly flagged "review manually".
      - confirm dialog stating it will back up first and disable N mods.
      - live run progress by polling `getMigration` (reuse the update-run polling pattern).
- [ ] `pnpm lint && pnpm build` clean.

## Phase 6 — Docs

- [ ] Note the feature + the backup-restore rollback contract in `docs/` and this file's
      Notes section.

---

## Notes / limitations (to fill in as built)

- CurseForge and custom jars can't be auto-checked (no reliable version listing /
  no source project), so they're surfaced as "unmanaged" and left untouched — the
  operator decides. They are the main reason a migrated server might still need a
  manual pass.
- Rollback consistency depends on the snapshot in Phase 3 step 1: the filesystem
  restore alone won't undo `installed_mods` / `mc_version` rows, so both must be
  rewritten together.
- v1 does not change platform or do per-mod culprit isolation; a bad boot rolls the
  whole migration back.
