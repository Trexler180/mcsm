# Master Plan — Modrinth integration + QoL

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done

## Phase 1 — C5: Git safety net
- [x] `git init`, `.gitignore` (db/servers/build caches excluded)
- [x] initial snapshot commit

## Phase 2 — A1–A6: Modrinth bug batch (+ tests)
- [x] A1 facet composition fixed (loader + version + server_side all apply)
- [x] A2 platform → modrinth loader mapping on install (LoaderForPlatform)
- [x] A3 download via context client (modrinth.Download), no bare http.Get
- [x] A4 SHA256 verification of downloaded jar, reject mismatch
- [x] A5 stream download → temp file → multipart (no io.ReadAll of whole jar)
- [x] A6 uninstall checks agent delete status before DB delete
- [x] tests: facet builder, loader mapping, SHA verify

## Phase 3 — B1: Correct install dir per platform/type
- [x] migration 002: install_path + installed_as_dep columns
- [x] installDirForVersion → /mods, /plugins, /world/datapacks
- [x] install_path stored + used on uninstall/update

## Phase 4 — B3 updates/pinning + B2 dependency resolution
- [x] GET /mods/updates, POST /mods/{id}/update, POST /mods/{id}/pin
- [x] transitive required-dependency install (installRecursive, cycle guard)
- [x] UI: updates badge, update-all, pin toggle, dep count toast

## Phase 5 — B4/B5 search UX
- [x] B4 project_type selector (mod/plugin/datapack/modpack/shader/resourcepack)
- [x] B5 sort index, version picker dropdown, offset plumbed
- [x] client SearchParams + handler + UI

## Phase 6 — C1–C4 QoL
- [x] C1 audit log writes (auth/server/mod actions) + admin audit view + per-server endpoint
- [x] C2 backup restore (agent+api+UI) + retention enforcement in scheduler/manual
- [x] C3 scheduled-task UI — already present (TasksTab), verified wired to api.tasks
- [x] C4 slog structured logging + request-id middleware (api+agent) + agent graceful instance shutdown

## Phase 7 — B6/C6
- [x] B6 modpack (.mrpack) install — manifest files + overrides, server-side only
- [x] C6 CurseForge source behind CURSEFORGE_API_KEY (normalized to modrinth shapes) + source toggle UI

---

## Notes / limitations
- CurseForge: no dependency auto-resolution and no SHA256 verify (CF exposes sha1/sha512 only); files with author-disabled distribution return a clear error.
- Modpack install is Modrinth-only (.mrpack). CurseForge modpacks (manifest.json) not handled.
- Mod "updates" check is Modrinth-only; CF-installed mods are skipped.
- Backup retention: keep_last_n + max_age_days from default target's retention JSON, fallback keep 10.

## Loader/type mapping reference
Platform → Modrinth loader: vanilla→(none), paper/purpur/spigot/bukkit→same, fabric/quilt/forge/neoforge→1:1.
project_type → dir: mod→/mods, plugin→/plugins, datapack→/world/datapacks.
