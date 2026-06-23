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

## Phase 2 — Migration store + run model  ✅ DONE

- [x] Migration `apps/api/migrations/010_version_migrations.sql`:
      `server_version_migrations(id, server_id, from_mc_version, to_mc_version,
      backup_id, status, detail JSON, started_at, finished_at)` + `server_id` index.
      (Path note: migrations live at `apps/api/migrations/`, not `internal/migrations/`.)
- [x] `store/migration.go`: `CreateVersionMigration`, `UpdateVersionMigration` (also
      records `backup_id` via COALESCE), `GetVersionMigration`, `ListVersionMigrations` —
      mirrors `store/autoupdate.go`.
- [x] Covered by `migrations_test.go` (applies cleanly in the suite).

## Phase 3 — Migration engine (apply, atomic via backup)  ✅ DONE

- [x] New `internal/migrate` package with a per-server in-flight guard
      (`ErrAlreadyRunning`).
- [x] `Trigger(ctx, serverID, targetMC)` → creates the run row, returns it, runs the
      rest in a **detached** goroutine with a hard timeout.
- [x] `execute` flow, writing phase/message to the run detail throughout:
      1. **snapshot** rollback state in memory: `srv.MCVersion`/`LoaderVersion` + every
         `installed_mods` row.
      2. `buildPlan` **re-checks** compatibility server-side (not the client payload);
         classifies into update / disable / unchanged / unmanaged / unknown.
      3. record prior power state; stop the server.
      4. **backup**: `CreateBackup` row + `agent.Backup`; abort if it fails (best-effort
         restart if it was running).
      5. set `srv.MCVersion` + persist; `agent.Reinstall` the runtime jar.
      6. update mods → `swapFile`; incompatible enabled mods → `RenameFile` `.disabled` +
         `SetModEnabled(false)`.
      7. start + `startAndWatch`.
      8. **healthy** → restore prior power state, status `success` (`partial` if some mod
         steps failed). **unhealthy / apply error** → `rollback`: `agent.Restore(backup)` +
         rewrite snapshot rows + `mc_version`/loader, status `reverted` (`failed` only if
         the restore itself fails).
- [x] Added `agent.Client.RenameFile` (the engine needs a rename to disable jars).
- [x] Audit actions: `server.migrate`, `server.migrate_reverted`, `server.migrate_failed`.
- [x] Tests (`engine_test.go`): healthy migrate (update + disable), unhealthy→restore,
      backup-failure abort, concurrent-run guard. Fake agent models backup/restore
      snapshots so the rollback path is exercised end to end.

Decisions made during build:
- **Loader bump deferred**: the agent's reinstall derives the loader itself (its
  `install.Reinstall` takes no loader version), so the engine passes the same cfg as the
  existing `ServerHandlers.Reinstall` and does not set `loader_version`. Revisit only if
  the agent ever needs it explicitly.
- **`unmanaged`/`unknown` mods are left fully untouched** during apply (not disabled),
  matching the preview's "review manually" contract.
- Pinned mods *are* moved (a version change is deliberate), consistent with the locked
  decision.

Not yet wired: the engine has no HTTP surface yet — that's **Phase 4** (trigger +
history/poll endpoints, construct the engine in `main.go`/`NewRouter`).

## Phase 4 — API surface  ✅ DONE

- [x] `POST /servers/{id}/migrate` (body `{mc_version}`) → engine `Trigger`, gated
      `settingsAccess` (same leaf as `Reinstall`). Soft-validates the target and rejects a
      no-op (target == current). Returns 202 with the run row.
- [x] `GET /servers/{id}/migrations` + `GET /servers/{id}/migrations/{runId}` for history
      and live polling, gated `viewAccess`.
- [x] `handlers/migrations.go` (`MigrationHandlers`); engine constructed inside `NewRouter`
      via `migrate.New(s)` (router-only dependency, so no `main.go` signature change).

## Phase 5 — Web UI  ✅ DONE

- [x] `lib/api.ts`: `servers.migrate` / `servers.migrations` / `servers.migration`
      (`versionCheck` already existed). Types `VersionMigration` + `VersionMigrationDetail`
      + `MigrationModStep` in `lib/types.ts`.
- [x] `VersionCheckDialog` extended from a preview into the full flow:
      - target picker + upgrade/downgrade indicator + ratio bar + grouped per-bucket lists
        (from Phase 1).
      - inline confirm step spelling out the backup, N updated / M disabled, and the
        "K items can't be checked — review manually" warning.
      - applies via `servers.migrate`, then polls `servers.migration` (1.5s while running)
        and renders live phase/message + per-mod step rows; "Run in background" lets the
        operator close without aborting; terminal state invalidates mods/server/backups.
- [x] `pnpm lint` (0 errors) + `pnpm build` clean.

## Phase 6 — Docs  ✅ DONE

- [x] Added a "Version Migration" section to `docs/operations.md` covering the preview
      buckets, the atomic apply flow, and the backup-restore rollback contract.

---

## Status: COMPLETE

All six phases shipped (commits `559cb06`, `44d0bb6`, `b7921b9`, + docs). The feature is
usable end to end: preview → confirm → apply → live progress → auto-rollback.

Decisions locked during build (differing from / refining the original plan):
- **Permission**: single `settingsAccess` gate (same as Reinstall), not a settings+mods+
  backups combination. Tighten later if desired.
- **`unknown` mods** (transient lookup failure): surfaced as an informational warning in
  the confirm step, not a hard block; left untouched on apply and caught by rollback if
  one turns out incompatible.
- **Loader bump dropped**: the agent's reinstall derives the loader itself.

Possible follow-ups (not built): refresh the preview against live state after an apply;
expose migration history in the UI (the endpoint exists); a pre-apply hard gate on
`unknown` mods; platform switching (Paper↔Purpur, Fabric↔Quilt).

## Notes / limitations

- CurseForge and custom jars can't be auto-checked (no reliable version listing /
  no source project), so they're surfaced as "unmanaged" and left untouched — the
  operator decides. They are the main reason a migrated server might still need a
  manual pass.
- Rollback consistency depends on the snapshot in Phase 3 step 1: the filesystem
  restore alone won't undo `installed_mods` / `mc_version` rows, so both must be
  rewritten together.
- v1 does not change platform or do per-mod culprit isolation; a bad boot rolls the
  whole migration back.
