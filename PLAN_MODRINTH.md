# Master Plan — Modrinth integration + QoL

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done

Implementation order (each phase builds + commits independently):

## Phase 1 — C5: Git safety net
- [ ] `git init`, add `.gitignore` (bin/, node_modules/, dist/, mcsm.db*, .buildtools/, servers/)
- [ ] initial snapshot commit

## Phase 2 — A1–A6: Modrinth bug batch (+ tests)
- [ ] A1 fix search facet composition (loader + version + server_side combine, no overwrite)
- [ ] A2 map server platform → modrinth loader on install (always, not only when LoaderVersion!=nil)
- [ ] A3 download via http.NewRequestWithContext + shared client (no bare http.Get)
- [ ] A4 verify SHA256 of downloaded jar, reject mismatch
- [ ] A5 stream download → multipart (io.Pipe) instead of io.ReadAll
- [ ] A6 uninstall checks agent delete status before DB delete
- [ ] tests: client facet builder, loader mapping, sha verify

## Phase 3 — B1: Correct install dir per platform/type
- [ ] migration 002: add `install_path` column to installed_mods
- [ ] map (project_type, platform) → target dir (/mods, /plugins, /world/datapacks)
- [ ] store + use install_path on uninstall
- [ ] store funcs + model field

## Phase 4 — B3 updates/pinning + B2 dependency resolution
- [ ] B3 GET /servers/{id}/mods/updates (compare latest compatible vs installed)
- [ ] B3 POST /servers/{id}/mods/{modId}/update (swap jar)
- [ ] B3 POST /servers/{id}/mods/{modId}/pin (toggle pinned)
- [ ] B2 resolve required deps transitively on install (migration: installed_as_dep flag)
- [ ] UI: updates badge, update-all, pin toggle, dep confirm

## Phase 5 — B4/B5 search UX
- [ ] B4 project_type selector (mod/plugin/datapack/modpack/shader/resourcepack)
- [ ] B5 sort, category facets, pagination (offset), version picker dropdown
- [ ] client + handler + UI

## Phase 6 — C1–C4 QoL
- [ ] C1 audit log: WriteAudit wired to mod/server/auth/backup actions + GET /audit
- [ ] C2 backup restore endpoint (agent + api) + retention enforcement in scheduler
- [ ] C3 scheduled-task React route
- [ ] C4 slog structured logging + request-id middleware

## Phase 7 — B6/C6 (big, optional)
- [ ] B6 modpack (.mrpack) install
- [ ] C6 CurseForge source behind CURSEFORGE_API_KEY

---

## Loader/type mapping reference

Platform → Modrinth loader facet:
- vanilla → (none, datapacks only)
- paper/purpur/spigot/bukkit → paper / spigot / bukkit / purpur (plugins)
- fabric → fabric
- quilt → quilt
- forge → forge
- neoforge → neoforge

project_type → install dir:
- mod → /mods
- plugin → /plugins
- datapack → /world/datapacks
- resourcepack/shader → /resourcepacks /shaderpacks (client-side; allow but warn)
