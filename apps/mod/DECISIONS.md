# MCSM Helper Mod — decisions and research

Status: **design locked, implementation in progress.**

This file records (a) the design decisions agreed with the maintainer and (b) the
verified toolchain facts behind them, with sources. Read this before changing the
mod, the wire protocol, or the agent side of the link.

---

## 1. Why this mod exists

Today the agent learns about a running server two ways, both weak:

- **Log scraping.** `apps/agent/internal/process/instance.go:23-25` regex-matches
  `joined the game` / `left the game` / `There are N players online`. This breaks
  on any mod that reformats chat or join messages, gives no UUIDs, and cannot
  express anything the vanilla log does not already print.
- **File poking.** Player detail comes from on-disk NBT and `stats/<uuid>.json`,
  which are only written when the world saves or the player logs out — so live
  data is stale by construction.

Actions go the other way, by writing text to the process stdin
(`instance.go:877`) and hoping. There is no success/failure signal and no way to
read a command's reply.

The mod replaces both directions with a structured, authenticated, push-capable
channel — while the scraping path stays as the fallback for servers that do not
or cannot run it (vanilla, Paper, mod disabled).

---

## 2. Locked decisions

| Area | Decision |
|---|---|
| Platform | **Fabric first.** Core (protocol, auth, serialization, queueing) is platform-agnostic; a thin adapter binds it to loader events, so Paper/NeoForge are additions rather than a rewrite. |
| Transport | Mod **dials out** to the agent over WebSocket. Bidirectional: agent sends RPC requests, mod pushes events. No inbound port to allocate, no discovery, works when the server has outbound-only access. |
| Provisioning | Agent injects `MCSM_AGENT_URL`, `MCSM_SERVER_ID`, `MCSM_TOKEN` into the java process it already spawns. Token is **per-launch**, and is persisted in agent runstate so a **reattached** server can re-authenticate after an agent restart. |
| Data strategy | Mod is the **preferred** source; log/file scraping is the fallback. Fields carry provenance. |
| Reads (v1) | Push join/leave/death with real UUIDs; periodic vitals heartbeat — TPS, MSPT percentiles, heap + GC, chunk/entity totals, uptime, online list with ping and dimension. |
| Writes (v1) | Typed RPCs (kick, ban, whitelist, op/deop, message, save-all, stop) returning real success/failure, **plus** a generic command exec that returns captured output. |
| Implementation | **Fabric API events only. Zero mixins.** |
| Distribution | Jar is `go:embed`ded into `mcsm-agent` and auto-installed into the server's `mods/`. |
| Consent | **Opt-in per server** (default on for panel-created servers). Reconcile learns a manager-owned flag: the jar is visible in the mod list but is not update-checked, and removing it flips the toggle off instead of letting the agent silently reinstall it. |
| Build | Gradle is the source of truth; the built jar is **committed**; jar output is reproducible; CI rebuilds and verifies by hash. |
| Schema | DTOs hand-written on both sides, kept honest by **shared golden fixtures** that both the Go and the Java tests parse and re-serialize. |
| Failure policy | **Never block the server tick thread.** Bounded queue, drop on overflow, backoff reconnect, rate-limited logging. Correctness is restored by full **snapshots** on every heartbeat and every reconnect — the agent reconciles to truth rather than replaying history. |
| Metrics | Extend the existing metric series with nullable `tps` / `mspt_p95` / `heap_used_mb` / `chunks` / `entities`, heartbeat aligned to the existing sample interval. |
| Testing | Fixture + fake-peer tests in PR CI; real-Fabric-server end-to-end on a schedule. |

### Why snapshots, not just events

`apps/api/internal/store/player_sessions.go` and `uptime.go` persist durable
records derived from join/leave. If an event is dropped under backpressure, a
player is "online forever" in the database. Shipping a full online-list snapshot
on every heartbeat makes a dropped event self-correcting within one interval,
which is what allows the drop-on-overflow policy to be safe.

### Why zero mixins

The agent already ships `mixincrash.go` and `modconflict.go` — dedicated
detection for mods that break servers via mixins. Shipping a mixin-heavy mod of
our own would make us the thing our own product warns users about. Fabric API
events cover everything v1 needs.

---

## 3. Verified toolchain facts (July 2026)

Every value below was confirmed against a primary source on 2026-07-29. Do not
"update" these from memory — re-query the source.

| Fact | Value | Source |
|---|---|---|
| Current MC release | **26.2** (released 2026-06-16) | `piston-meta.mojang.com/mc/game/version_manifest_v2.json` |
| Current MC snapshot | 26.3-snapshot-6 | same |
| Required Java | **25** (`java-runtime-epsilon`) | `26.2.json` version manifest, `javaVersion` |
| Fabric Loader | **0.19.3** (stable) | `meta.fabricmc.net/v2/versions/loader` |
| Fabric API for 26.2 | **0.156.0+26.2** (example template pins 0.155.2+26.2) | Modrinth API, project `P7dR8mSH` |
| Fabric Loom | **1.17.17** (latest release; template uses `1.17-SNAPSHOT`) | `maven.fabricmc.net/net/fabricmc/fabric-loom/maven-metadata.xml` |
| Gradle | **9.5.1** | `fabric-example-mod@26.2` wrapper properties |
| Official template | `github.com/FabricMC/fabric-example-mod` branch **`26.2`** | GitHub |

### Minecraft is no longer obfuscated

This is the single most consequential finding and it changes how the mod is built.

Evidence:

1. `1.21.11`'s version manifest lists `client, client_mappings, server, server_mappings`.
   `26.2`'s lists only `client, server` — the ProGuard mapping files are **gone**.
2. `meta.fabricmc.net/v2/versions/yarn` stops at **1.21.11**. No yarn build exists
   for any 26.x version.
3. `meta.fabricmc.net/v2/versions/intermediary/26.2` returns a placeholder `0.0.0`.
4. The official `fabric-example-mod@26.2` `build.gradle` has **no `mappings`
   dependency line at all**. Every 1.21.x template had
   `mappings "net.fabricmc:yarn:${yarn_mappings}:v2"`.

Consequences:

- No yarn/intermediary dependency, no remapping step, real Minecraft names in source.
- Cross-version source compatibility is far more plausible than it was under yarn,
  which strengthens the zero-mixins bet.
- **The reproducible-jar risk is largely defused.** Without `remapJar` rewriting
  bytecode, deterministic output is just standard Gradle `Jar` configuration
  (`preserveFileTimestamps = false`, `reproducibleFileOrder = true`). This was
  flagged as the main threat to the commit-the-jar + CI-hash scheme; it is much
  smaller than feared. Still verify empirically before trusting the CI gate.

### Deliberate deviations from the official template

- **Pin Loom to `1.17.17`, not `1.17-SNAPSHOT`.** A snapshot dependency changes
  underneath you, which is incompatible with committing a jar and verifying it by
  hash. Reproducibility requires pinned inputs.
- **No `splitEnvironmentSourceSets()`, no client source set, no mixin configs.**
  This is a server-only mod (`"environment": "server"`); the client scaffolding in
  the template is dead weight.
- **No external Java dependencies.** Java 25 ships `java.net.http.WebSocket`, so
  the WebSocket client needs no shaded library. The jar stays tiny, which matters
  because it is embedded in the agent binary and committed to git.

---

## 4. Open items

- **Version range.** v1 targets 26.2 only. Whether one jar also loads on 26.1.x /
  26.3 is untested; widen `depends.minecraft` only with evidence.
- **Generic exec threading.** Commands must run on the server thread to be correct,
  but the socket thread must never wait on it. Exec is therefore async with a
  correlation id and a timeout. This is the one place the two design principles
  collide and it must be right in the protocol, not patched later.
- **Reproducibility must be proven**, not assumed, before the CI hash gate is
  trusted. If Loom or Gradle injects nondeterminism that cannot be suppressed, the
  gate degrades to "the mod still compiles" — weaker, and it must then be labelled
  as such rather than quietly passing.
